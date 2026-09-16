package gatewaystub

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/lib/gateway/frame"
	"github.com/coder/websocket"
)

func dial(t *testing.T, s *Stub, path, token string) (*websocket.Conn, *http.Response) {
	t.Helper()
	ws, resp, err := websocket.Dial(context.Background(), s.URL()+path, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		return nil, resp
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	return ws, resp
}

func roundTrip(t *testing.T, ws *websocket.Conn, f frame.Frame) frame.Frame {
	t.Helper()
	b, _ := f.Encode()
	if err := ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	r, err := frame.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func waitClose(t *testing.T, ws *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, _, err := ws.Read(ctx)
		if err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func good() map[string]any {
	return map[string]any{"module": "m", "instance": "i", "version": "0", "address": "127.0.0.1:1", "prefixes": []string{"/api/v2/m"}, "capacity": 1}
}

func TestHandshake_RejectsBadToken(t *testing.T) {
	s := New(t, Options{Secret: "s", Audience: "gw"})
	ws, resp := dial(t, s, "/m/i", "nope")
	if ws != nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("ws=%v resp=%v", ws, resp)
	}
	if s.HandshakeRejections() != 1 {
		t.Fatal("not counted")
	}
}

func TestHandshake_RejectsBadPath(t *testing.T) {
	s := New(t, Options{Secret: "s", Audience: "gw"})
	if _, resp := dial(t, s, "/m", ServiceToken("s", "gw")); resp == nil || resp.StatusCode != 404 {
		t.Fatalf("%v", resp)
	}
	if _, resp := dial(t, s, "/M!/i", ServiceToken("s", "gw")); resp == nil || resp.StatusCode != 400 {
		t.Fatalf("%v", resp)
	}
}

func TestRegister_Refusals(t *testing.T) {
	cases := []struct {
		name string
		mod  func(map[string]any)
		code int
	}{
		{"missing field", func(p map[string]any) { delete(p, "capacity") }, 400},
		{"address not ipv4", func(p map[string]any) { p["address"] = "localhost:1" }, 400},
		{"address outside cidr", func(p map[string]any) { p["address"] = "8.8.8.8:1" }, 403},
		{"instance differs from url", func(p map[string]any) { p["instance"] = "other" }, 422},
		{"prefixes overlap", func(p map[string]any) { p["prefixes"] = []string{"/api/v2/m", "/api/v2/m/x"} }, 422},
		{"bad prefix", func(p map[string]any) { p["prefixes"] = []string{"/api/v1/m"} }, 400},
		{"capacity 0", func(p map[string]any) { p["capacity"] = 0 }, 422},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New(t, Options{Secret: "s", Audience: "gw"})
			ws, _ := dial(t, s, "/m/i", ServiceToken("s", "gw"))
			p := good()
			c.mod(p)
			r := roundTrip(t, ws, frame.NewCall("/register", p))
			if r.Type != frame.CallError || r.Code != c.code {
				t.Fatalf("got %+v, want CALLERROR %d", r, c.code)
			}
			if code := waitClose(t, ws); code != 1008 {
				t.Fatalf("close %d, want 1008", code)
			}
		})
	}
}

func TestRegister_GoodThenCallBeforeRegisterIs409(t *testing.T) {
	s := New(t, Options{Secret: "s", Audience: "gw"})
	ws, _ := dial(t, s, "/m/i", ServiceToken("s", "gw"))
	if r := roundTrip(t, ws, frame.NewCall("/heartbeat", map[string]any{"in_flight": 0})); r.Type != frame.CallError || r.Code != 409 {
		t.Fatalf("%+v", r)
	}
	if r := roundTrip(t, ws, frame.NewCall("/register", good())); r.Type != frame.CallResult {
		t.Fatalf("%+v", r)
	}
	if r := roundTrip(t, ws, frame.NewCall("/bogus", nil)); r.Type != frame.CallError || r.Code != 404 {
		t.Fatalf("%+v", r)
	}
}

func TestNoRegister_Closes1008(t *testing.T) {
	s := New(t, Options{Secret: "s", Audience: "gw", RegisterTimeout: 100 * time.Millisecond})
	ws, _ := dial(t, s, "/m/i", ServiceToken("s", "gw"))
	if code := waitClose(t, ws); code != 1008 {
		t.Fatalf("close %d", code)
	}
}

func TestHeartbeatTimeout_Closes4000(t *testing.T) {
	s := New(t, Options{Secret: "s", Audience: "gw", HeartbeatInterval: 1, OfflineAfter: 1})
	ws, _ := dial(t, s, "/m/i", ServiceToken("s", "gw"))
	if r := roundTrip(t, ws, frame.NewCall("/register", good())); r.Type != frame.CallResult {
		t.Fatalf("%+v", r)
	}
	if code := waitClose(t, ws); code != 4000 {
		t.Fatalf("close %d, want 4000", code)
	}
}

func TestSecondRegistration_ReplacesFirstWith1001(t *testing.T) {
	s := New(t, Options{Secret: "s", Audience: "gw"})
	first, _ := dial(t, s, "/m/i", ServiceToken("s", "gw"))
	roundTrip(t, first, frame.NewCall("/register", good()))
	second, _ := dial(t, s, "/m/i", ServiceToken("s", "gw"))
	if r := roundTrip(t, second, frame.NewCall("/register", good())); r.Type != frame.CallResult {
		t.Fatalf("%+v", r)
	}
	if code := waitClose(t, first); code != 1001 {
		t.Fatalf("first closed %d, want 1001", code)
	}
}
