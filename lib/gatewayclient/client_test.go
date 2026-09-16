package gatewayclient_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/internal/gatewaystub"
	"github.com/apostoldevel/go-platform/lib/gatewayclient"
)

const testSecret = "test-secret"

// module returns a client wired to the stub with fast reconnects.
func module(t *testing.T, stub *gatewaystub.Stub, mod func(*gatewayclient.Config)) (*gatewayclient.Client, *gatewayclient.Recorder) {
	t.Helper()
	rec := gatewayclient.NewRecorder()
	cfg := gatewayclient.Config{
		URL:      stub.URL(),
		Module:   "clients",
		Instance: "clients-1",
		Version:  "0.1.0",
		Address:  "127.0.0.1:8081",
		Prefixes: []string{"/api/v2/clients"},
		Capacity: 64,
		Token: func(context.Context) (string, error) {
			return gatewaystub.ServiceToken(testSecret, "gateway-test"), nil
		},
		OnEvent:   rec.Record,
		Reconnect: func(int) time.Duration { return 20 * time.Millisecond },
		InFlight:  func() int { return 0 },
	}
	if mod != nil {
		mod(&cfg)
	}
	c, err := gatewayclient.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, rec
}

// run starts c.Run in the background; the returned channel yields its result
// once and stays readable afterwards (closed), so Cleanup can wait too.
func run(t *testing.T, c *gatewayclient.Client) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() { done <- c.Run(ctx); close(finished) }()
	t.Cleanup(func() { cancel(); <-finished })
	return cancel, done
}

func TestRegister_BecomesReadyAndHeartbeats(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, rec := module(t, stub, nil)
	run(t, c)

	reg := stub.WaitRegistered(t, 2*time.Second)
	if reg.Module != "clients" || reg.Instance != "clients-1" || reg.Address != "127.0.0.1:8081" || reg.Capacity != 64 || reg.Prefixes[0] != "/api/v2/clients" {
		t.Fatalf("registration %+v", reg)
	}
	rec.Wait(t, gatewayclient.EventRegistered, 2*time.Second)
	if c.State() != gatewayclient.Ready {
		t.Fatalf("state %s, want ready", c.State())
	}
	// first heartbeat ≤ heartbeat_interval, then every interval
	if n := stub.WaitHeartbeats(t, 2, 3*time.Second); n < 2 {
		t.Fatalf("heartbeats %d", n)
	}
	if got := stub.LastHeartbeat().InFlight; got != 0 {
		t.Fatalf("in_flight %d", got)
	}
}

func TestRegister_UsesWSLocalAddrWhenAddressEmpty(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, _ := module(t, stub, func(cfg *gatewayclient.Config) { cfg.Address = ""; cfg.ListenPort = 9090 })
	run(t, c)
	reg := stub.WaitRegistered(t, 2*time.Second)
	host, port, err := net.SplitHostPort(reg.Address)
	if err != nil || host != "127.0.0.1" || port != "9090" {
		t.Fatalf("address %q (%v)", reg.Address, err)
	}
}

func TestRegisterError_1008_RetriesSlowly(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	stub.FailRegister(422, "prefixes overlap") // every time
	var slow atomic.Int32
	c, rec := module(t, stub, func(cfg *gatewayclient.Config) {
		cfg.ReconnectAfterRefusal = func() time.Duration { slow.Add(1); return 30 * time.Millisecond }
	})
	run(t, c)
	rec.WaitN(t, gatewayclient.EventRefused, 2, 3*time.Second)
	if slow.Load() < 1 {
		t.Fatal("refusal must use the slow schedule, not the backoff")
	}
	if stub.CloseCodes()[0] != 1008 {
		t.Fatalf("close codes %v", stub.CloseCodes())
	}
}

func TestDrop_ReconnectsAndReregisters(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, rec := module(t, stub, nil)
	run(t, c)
	rec.Wait(t, gatewayclient.EventRegistered, 2*time.Second) // the client, not only the stub, must have seen the result
	stub.DropConnection()
	rec.Wait(t, gatewayclient.EventDisconnected, 2*time.Second)
	if stub.WaitRegistrations(t, 2, 3*time.Second) < 2 {
		t.Fatal("no re-registration")
	}
	rec.WaitN(t, gatewayclient.EventRegistered, 2, 2*time.Second)
	if c.State() != gatewayclient.Ready {
		t.Fatalf("state %s", c.State())
	}
}

func TestReplaced_1001_DoesNotReconnect(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, rec := module(t, stub, nil)
	_, done := run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	stub.Replace() // closes the socket 1001 as a newer registration would
	rec.Wait(t, gatewayclient.EventReplaced, 2*time.Second)
	select {
	case err := <-done:
		if !errors.Is(err, gatewayclient.ErrReplaced) {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after 1001")
	}
	if n := stub.Registrations(); n != 1 {
		t.Fatalf("registrations %d, want 1", n)
	}
}

func TestSilentGateway_TwoUnansweredHeartbeats_Reconnects(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, rec := module(t, stub, nil)
	run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	stub.SilenceHeartbeats(true)
	rec.Wait(t, gatewayclient.EventDisconnected, 5*time.Second)
	stub.SilenceHeartbeats(false)
	if stub.WaitRegistrations(t, 2, 3*time.Second) < 2 {
		t.Fatal("no re-registration after silence")
	}
}

func TestPing_AnswersInFlight(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, _ := module(t, stub, func(cfg *gatewayclient.Config) { cfg.InFlight = func() int { return 7 } })
	run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	res := stub.Call(t, "/ping", nil, 2*time.Second)
	if res.Type != 3 || string(res.Payload) != `{"in_flight":7}` {
		t.Fatalf("ping result %+v %s", res, res.Payload)
	}
}

func TestUnknownAction_404(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, _ := module(t, stub, nil)
	run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	res := stub.Call(t, "/nope", nil, 2*time.Second)
	if res.Type != 4 || res.Code != 404 {
		t.Fatalf("%+v", res)
	}
}

func TestDrain_StatusThenWaitThenUnregisterThen1000(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	var inflight atomic.Int32
	inflight.Store(2)
	c, rec := module(t, stub, func(cfg *gatewayclient.Config) { cfg.InFlight = func() int { return int(inflight.Load()) } })
	_, done := run(t, c)
	stub.WaitRegistered(t, 2*time.Second)

	go func() { time.Sleep(300 * time.Millisecond); inflight.Store(0) }()
	if err := c.Drain(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	st := stub.WaitStatus(t, "draining", 2*time.Second)
	if st.Reason != "test" {
		t.Fatalf("status %+v", st)
	}
	unreg := stub.WaitUnregister(t, 2*time.Second)
	if unreg.Reason != "test" {
		t.Fatalf("unregister %+v", unreg)
	}
	if codes := stub.CloseCodes(); len(codes) == 0 || codes[len(codes)-1] != 1000 {
		t.Fatalf("close codes %v, want …1000", codes)
	}
	rec.Wait(t, gatewayclient.EventDrained, time.Second)
	if err := <-done; err != nil {
		t.Fatalf("Run after drain: %v", err)
	}
	if stub.Registrations() != 1 {
		t.Fatal("reconnected after own unregister")
	}
}

func TestDrainCommand_FromGateway(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, _ := module(t, stub, nil)
	_, done := run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	res := stub.Call(t, "/drain", map[string]any{"reason": "rollout", "deadline": 5}, 2*time.Second)
	if res.Type != 3 {
		t.Fatalf("drain result %+v", res)
	}
	stub.WaitStatus(t, "draining", 2*time.Second)
	if u := stub.WaitUnregister(t, 2*time.Second); u.Reason != "rollout" {
		t.Fatalf("unregister %+v", u)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDrain_DeadlineWinsOverInFlight(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, _ := module(t, stub, func(cfg *gatewayclient.Config) {
		cfg.InFlight = func() int { return 1 } // never drains
		cfg.DrainDeadline = 200 * time.Millisecond
	})
	_, done := run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	start := time.Now()
	_ = c.Drain(context.Background(), "stuck")
	stub.WaitUnregister(t, 2*time.Second)
	<-done
	if d := time.Since(start); d < 200*time.Millisecond || d > 1500*time.Millisecond {
		t.Fatalf("drain took %v", d)
	}
}

func TestReload_CallsHookAndReregistersWhenPrefixesChange(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	var reloads atomic.Int32
	c, _ := module(t, stub, func(cfg *gatewayclient.Config) {
		cfg.OnReload = func() gatewayclient.Registration {
			reloads.Add(1)
			return gatewayclient.Registration{Prefixes: []string{"/api/v2/clients", "/api/v2/drivers"}, Address: "127.0.0.1:8081", Capacity: 64}
		}
	})
	run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	res := stub.Call(t, "/reload", nil, 2*time.Second)
	if res.Type != 3 || reloads.Load() != 1 {
		t.Fatalf("%+v reloads=%d", res, reloads.Load())
	}
	if stub.WaitRegistrations(t, 2, 3*time.Second) < 2 {
		t.Fatal("no re-registration after prefixes change")
	}
	if got := stub.LastRegistration().Prefixes; len(got) != 2 {
		t.Fatalf("prefixes %v", got)
	}
}

func TestOverloaded_StatusBothWays(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, _ := module(t, stub, nil)
	run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	c.SetOverloaded(true)
	stub.WaitStatus(t, "overloaded", 2*time.Second)
	c.SetOverloaded(false)
	stub.WaitStatus(t, "ready", 2*time.Second)
}

func TestHandshake401_RefreshesTokenAndRetries(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	var calls atomic.Int32
	c, _ := module(t, stub, func(cfg *gatewayclient.Config) {
		cfg.Token = func(context.Context) (string, error) {
			if calls.Add(1) == 1 {
				return "garbage", nil
			}
			return gatewaystub.ServiceToken(testSecret, "gateway-test"), nil
		}
		cfg.ReconnectAfterRefusal = func() time.Duration { return 20 * time.Millisecond }
	})
	run(t, c)
	stub.WaitRegistered(t, 3*time.Second)
	if calls.Load() < 2 {
		t.Fatalf("token calls %d", calls.Load())
	}
	if stub.HandshakeRejections() != 1 {
		t.Fatalf("rejections %d", stub.HandshakeRejections())
	}
}

func TestDefaultReconnectSchedule(t *testing.T) {
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
	for i, w := range want {
		got := gatewayclient.DefaultReconnect(i)
		lo, hi := time.Duration(float64(w*time.Second)*0.8), time.Duration(float64(w*time.Second)*1.2)
		if got < lo || got > hi {
			t.Fatalf("attempt %d: %v not within ±20%% of %v", i, got, w*time.Second)
		}
	}
}

func TestGatewayGoneEntirely_KeepsRetrying(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := "ws" + srv.URL[len("http"):] + "/gateway"
	srv.Close()
	rec := gatewayclient.NewRecorder()
	c, err := gatewayclient.New(gatewayclient.Config{URL: url, Module: "m", Instance: "i", Version: "0", Address: "127.0.0.1:1", Prefixes: []string{"/api/v2/m"}, Capacity: 1,
		Token: func(context.Context) (string, error) { return "t", nil }, OnEvent: rec.Record, Reconnect: func(int) time.Duration { return 10 * time.Millisecond }, InFlight: func() int { return 0 }})
	if err != nil {
		t.Fatal(err)
	}
	run(t, c)
	rec.WaitN(t, gatewayclient.EventConnectFailed, 3, 2*time.Second)
}

// ── after the K2 amendment of the contract ────────────

func TestOversizedFrameFromGateway_Closes1009AndReconnects(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, rec := module(t, stub, nil)
	run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	// just above MaxSize, below the socket read limit: frame.Parse decides, not the library
	stub.SendRaw(t, []byte(`{"t":2,"u":"6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11","a":"/ping","p":{"x":"`+strings.Repeat("a", 64*1024+200)+`"}}`))
	rec.Wait(t, gatewayclient.EventDisconnected, 2*time.Second)
	if codes := stub.WaitCloseCodes(t, 1, 2*time.Second); codes[0] != 1009 {
		t.Fatalf("close codes %v, want 1009 first", codes)
	}
	stub.WaitRegistrations(t, 2, 3*time.Second)
}

func TestGatewayCloses4000_Reconnects(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1, OfflineAfter: 1})
	c, rec := module(t, stub, nil)
	run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	stub.SilenceHeartbeats(true)
	stub.Kick(4000, "heartbeat timeout")
	rec.Wait(t, gatewayclient.EventDisconnected, 2*time.Second)
	stub.SilenceHeartbeats(false)
	stub.WaitRegistrations(t, 2, 3*time.Second)
}

func TestGatewayCloses1011_Reconnects(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, rec := module(t, stub, nil)
	run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	stub.Kick(1011, "internal error")
	rec.Wait(t, gatewayclient.EventDisconnected, 2*time.Second)
	stub.WaitRegistrations(t, 2, 3*time.Second)
}

func TestReload_WithoutChanges_DoesNotReregister(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, _ := module(t, stub, func(cfg *gatewayclient.Config) {
		cfg.OnReload = func() gatewayclient.Registration {
			return gatewayclient.Registration{Prefixes: []string{"/api/v2/clients"}}
		}
	})
	run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	stub.Call(t, "/reload", nil, 2*time.Second)
	time.Sleep(300 * time.Millisecond)
	if n := stub.Registrations(); n != 1 {
		t.Fatalf("registrations %d, want 1", n)
	}
}

func TestSilentGateway_ModuleClosesWith4001NotReplaced(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, rec := module(t, stub, nil)
	run(t, c)
	stub.WaitRegistered(t, 2*time.Second)
	stub.SilenceHeartbeats(true)
	rec.Wait(t, gatewayclient.EventDisconnected, 5*time.Second)
	if codes := stub.WaitCloseCodes(t, 1, 2*time.Second); codes[0] != 4001 {
		t.Fatalf("module must close 4001 on a silent gateway, got %v", codes)
	}
}

func TestClose1008AfterUpgrade_IsHandshakeRejection(t *testing.T) {
	// K2 amendment: until the gateway can refuse before
	// 101, the gateway upgrades and immediately closes 1008 with the reason.
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1, RejectAfterUpgrade: true})
	var calls atomic.Int32
	c, rec := module(t, stub, func(cfg *gatewayclient.Config) {
		cfg.Token = func(context.Context) (string, error) {
			if calls.Add(1) == 1 {
				return "garbage", nil
			}
			return gatewaystub.ServiceToken(testSecret, "gateway-test"), nil
		}
		cfg.ReconnectAfterRefusal = func() time.Duration { return 20 * time.Millisecond }
	})
	run(t, c)
	rec.Wait(t, gatewayclient.EventHandshakeRejected, 2*time.Second)
	stub.WaitRegistered(t, 3*time.Second)
	if stub.CloseCodes()[0] != 1008 {
		t.Fatalf("%v", stub.CloseCodes())
	}
}

func TestRunTwice_DrainTwice_NoPanic(t *testing.T) {
	stub := gatewaystub.New(t, gatewaystub.Options{Secret: testSecret, Audience: "gateway-test", HeartbeatInterval: 1})
	c, _ := module(t, stub, nil)
	for i := 0; i < 2; i++ {
		_, done := run(t, c)
		stub.WaitRegistrations(t, i+1, 2*time.Second)
		if err := c.Drain(context.Background(), "again"); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestCustomHTTPClientNeedsAddress(t *testing.T) {
	_, err := gatewayclient.New(gatewayclient.Config{URL: "ws://x/gateway", Module: "m", Instance: "i", Version: "0", ListenPort: 1, Prefixes: []string{"/api/v2/m"}, Capacity: 1,
		Token: func(context.Context) (string, error) { return "t", nil }, HTTPClient: &http.Client{}})
	if err == nil {
		t.Fatal("a custom HTTPClient cannot capture the local address; Address must be explicit")
	}
}
