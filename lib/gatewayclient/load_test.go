package gatewayclient_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/internal/gatewaystub"
	"github.com/apostoldevel/go-platform/lib/gatewayclient"
)

// loadModule is a module whose in-flight count is its Load.
func loadModule(t *testing.T, stub *gatewaystub.Stub, capacity int) (*gatewayclient.Client, *gatewayclient.Load, *gatewayclient.Recorder) {
	t.Helper()
	c, rec := module(t, stub, func(cfg *gatewayclient.Config) {
		cfg.Capacity = capacity
		cfg.InFlight = nil
	})
	return c, c.Load(), rec
}

func add(l *gatewayclient.Load, d, times int) {
	for range times {
		l.Add(int32(d))
	}
}

func TestLoad_OverloadedAtCapacity_ReadyAtThreeQuarters(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, l, rec := loadModule(t, stub, 8)
	run(t, c)
	rec.Wait(t, gatewayclient.EventRegistered, 2*time.Second)
	add(l, 1, 7)
	add(l, 1, 1) // 8 = capacity
	stub.WaitStatus(t, "overloaded", 2*time.Second)
	add(l, -1, 1) // 7: inside the gap, stays overloaded
	add(l, -1, 1) // 6 = 8 - 8/4
	stub.WaitStatus(t, "ready", 2*time.Second)
	if got := stub.Statuses(); !slices.Equal(got, []string{"overloaded", "ready"}) {
		t.Fatalf("statuses %v, want [overloaded ready]", got)
	}
}

func TestLoad_ReportedAtPing(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, l, rec := loadModule(t, stub, 64)
	run(t, c)
	rec.Wait(t, gatewayclient.EventRegistered, 2*time.Second)
	add(l, 1, 3)
	res := stub.Call(t, "/ping", map[string]any{}, 2*time.Second)
	var p struct {
		InFlight int `json:"in_flight"`
	}
	if err := json.Unmarshal(res.Payload, &p); err != nil || p.InFlight != 3 {
		t.Fatalf("ping %s, want in_flight 3", res.Payload)
	}
}

// draining → ready is not a transition the gateway accepts: the requests
// finishing during a drain must not send one.
func TestLoad_NoReadyWhileDraining(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, l, rec := loadModule(t, stub, 2)
	run(t, c)
	rec.Wait(t, gatewayclient.EventRegistered, 2*time.Second)
	add(l, 1, 2)
	stub.WaitStatus(t, "overloaded", 2*time.Second)
	drained := make(chan error, 1)
	go func() { drained <- c.Drain(context.Background(), "test") }()
	stub.WaitStatus(t, "draining", 2*time.Second)
	add(l, -1, 2)
	select {
	case err := <-drained:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not finish when the count fell to zero")
	}
	stub.WaitUnregister(t, 2*time.Second)
	if got := stub.Statuses(); !slices.Equal(got, []string{"overloaded", "draining"}) {
		t.Fatalf("statuses %v, want [overloaded draining]", got)
	}
}

// waitState polls the client's state: it changes after the gateway answered
// the /status the stub has already recorded.
func waitState(t *testing.T, c *gatewayclient.Client, want gatewayclient.State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for c.State() != want {
		if time.Now().After(deadline) {
			t.Fatalf("state %s, want %s", c.State(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLoad_StateFollowsTheStatus(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, l, rec := loadModule(t, stub, 1)
	run(t, c)
	rec.Wait(t, gatewayclient.EventRegistered, 2*time.Second)
	l.Add(1)
	stub.WaitStatus(t, "overloaded", 2*time.Second)
	waitState(t, c, gatewayclient.Overloaded)
	l.Add(-1)
	stub.WaitStatus(t, "ready", 2*time.Second)
	waitState(t, c, gatewayclient.Ready)
}

// A reconnection tells the new socket the flag as it is at registration:
// still overloaded — said again; fell while offline — nothing to say.
func TestLoad_Reconnect_TellsTheCurrentFlag(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, l, rec := loadModule(t, stub, 2)
	run(t, c)
	rec.Wait(t, gatewayclient.EventRegistered, 2*time.Second)
	add(l, 1, 2)
	stub.WaitStatus(t, "overloaded", 2*time.Second)
	stub.Kick(1001, "going away")
	rec.WaitN(t, gatewayclient.EventRegistered, 2, 3*time.Second)
	waitState(t, c, gatewayclient.Overloaded)
	if got := stub.Statuses(); !slices.Equal(got, []string{"overloaded", "overloaded"}) {
		t.Fatalf("statuses %v, want the flag said again on the new socket", got)
	}

	stub.Kick(1001, "going away")
	rec.Wait(t, gatewayclient.EventReplaced, 2*time.Second)
	add(l, -1, 2) // falls to zero while (or just after) reconnecting
	rec.WaitN(t, gatewayclient.EventRegistered, 3, 3*time.Second)
	waitState(t, c, gatewayclient.Ready)
	got := stub.Statuses()
	if n := len(got); n < 2 || got[n-1] == "overloaded" && n > 2 {
		t.Fatalf("statuses %v: the last word must not be overloaded", got)
	}
}
