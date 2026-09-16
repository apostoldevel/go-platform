// Package gatewaystub is a test double of GatewayAPI's control plane, written
// to the contract (docs/wiki/common/gateway-contract.md, K1–K6). It is the
// "self-check without the other side" of the Go module: every refusal the
// contract names can be produced on purpose, and everything the module sends
// is recorded for assertions.
package gatewaystub

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/lib/auth/jwt"
	"github.com/apostoldevel/go-platform/lib/gateway/frame"
	"github.com/coder/websocket"
)

// Options configures the stub; zero values take the contract's defaults.
type Options struct {
	Secret            string // HMAC secret of the gateway audience
	Audience          string // client_id the token must carry in aud
	HeartbeatInterval int    // seconds, default 5
	SuspectAfter      int    // default 2
	OfflineAfter      int    // default 4
	RegisterTimeout   time.Duration
	// RejectAfterUpgrade mirrors today's GatewayAPI (K2 amendment, T266): a
	// bad token is not refused with HTTP 401 but with close 1008
	// "unauthorized" right after 101, before any CALLRESULT.
	RejectAfterUpgrade bool
}

// Registration is what a module sent in /register.
type Registration struct {
	Module   string   `json:"module"`
	Instance string   `json:"instance"`
	Version  string   `json:"version"`
	Build    string   `json:"build"`
	Address  string   `json:"address"`
	Prefixes []string `json:"prefixes"`
	Capacity int      `json:"capacity"`
}

// Heartbeat is one /heartbeat payload.
type Heartbeat struct {
	InFlight int     `json:"in_flight"`
	Load     float64 `json:"load"`
}

// Status is one /status payload.
type Status struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

// Unregister is the /unregister payload.
type Unregister struct {
	Reason string `json:"reason"`
}

// Stub is one running gateway stub.
type Stub struct {
	t    testing.TB
	opts Options
	srv  *httptest.Server

	mu            sync.Mutex
	conn          *conn
	registrations []Registration
	heartbeats    []Heartbeat
	statuses      []Status
	unregisters   []Unregister
	closeCodes    []int
	rejections    int
	failCode      int
	failMsg       string
	silent        bool
	changed       *sync.Cond
}

type conn struct {
	ws       *websocket.Conn
	pending  map[string]chan frame.Frame
	lastBeat time.Time
	closed   bool
	instance string
}

var segment = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
var prefix = regexp.MustCompile(`^/api/v2/[a-z0-9_/-]+$`)

// New starts a stub on a test server; it stops with the test.
func New(t testing.TB, opts Options) *Stub {
	s := newStub(t, opts)
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(func() {
		s.srv.CloseClientConnections()
		s.srv.Close()
	})
	return s
}

// newStub builds the stub without listening anywhere.
func newStub(t testing.TB, opts Options) *Stub {
	if opts.HeartbeatInterval == 0 {
		opts.HeartbeatInterval = 5
	}
	if opts.SuspectAfter == 0 {
		opts.SuspectAfter = 2
	}
	if opts.OfflineAfter == 0 {
		opts.OfflineAfter = 4
	}
	if opts.RegisterTimeout == 0 {
		opts.RegisterTimeout = 5 * time.Second
	}
	s := &Stub{t: t, opts: opts}
	s.changed = sync.NewCond(&s.mu)
	return s
}

// URL is the control-plane base URL (ws://host:port/gateway).
func (s *Stub) URL() string { return "ws" + strings.TrimPrefix(s.srv.URL, "http") + "/gateway" }

// ServiceToken signs a token the way AuthServer would for the gateway audience.
func ServiceToken(secret, audience string) string {
	return jwt.Sign(jwt.Claims{Iss: "accounts.test", Aud: audience, Sub: strings.Repeat("a", 40), Iat: time.Now().Unix(), Exp: time.Now().Add(24 * time.Hour).Unix()}, "HS256", []byte(secret))
}

func (s *Stub) handle(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/gateway/"), "/")
	if !strings.HasPrefix(r.URL.Path, "/gateway/") || len(parts) != 2 {
		http.Error(w, `{"type":"urn:apostol:gateway:no-route","status":404}`, http.StatusNotFound)
		return
	}
	if !segment.MatchString(parts[0]) || !segment.MatchString(parts[1]) {
		http.Error(w, `{"type":"urn:apostol:gateway:bad-segment","status":400}`, http.StatusBadRequest)
		return
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	kr := jwt.Keyring{Audiences: map[string]jwt.Key{s.opts.Audience: {Alg: "HS256", Secret: []byte(s.opts.Secret)}}, Issuers: []string{"accounts.test"}}
	_, verr := kr.Verify(tok, time.Now())
	if verr != nil {
		s.mu.Lock()
		s.rejections++
		s.changed.Broadcast()
		s.mu.Unlock()
		if !s.opts.RejectAfterUpgrade {
			w.Header().Set("Content-Type", "application/problem+json")
			http.Error(w, `{"type":"urn:apostol:gateway:token-invalid","status":401}`, http.StatusUnauthorized)
			return
		}
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c := &conn{ws: ws, pending: map[string]chan frame.Frame{}}
	if verr != nil {
		s.close(c, 1008, "unauthorized")
		return
	}
	s.serve(c, parts[0], parts[1])
}

func (s *Stub) serve(c *conn, module, instance string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registered := false
	regTimer := time.AfterFunc(s.opts.RegisterTimeout, func() {
		if !registered {
			s.close(c, 1008, "no /register within timeout")
		}
	})
	defer regTimer.Stop()
	for {
		typ, data, err := c.ws.Read(ctx)
		if err != nil {
			s.observeClose(c, err)
			return
		}
		if typ != websocket.MessageText {
			s.close(c, 1003, "binary frame")
			return
		}
		f, err := frame.Parse(data)
		if err == frame.ErrTooLarge {
			s.close(c, 1009, "frame too large")
			return
		}
		if err != nil {
			s.send(c, frame.Frame{Type: frame.CallError, ID: "00000000-0000-4000-8000-000000000000", Code: 400, Message: err.Error()})
			continue
		}
		switch f.Type {
		case frame.CallResult, frame.CallError:
			s.mu.Lock()
			ch := c.pending[f.ID]
			delete(c.pending, f.ID)
			s.mu.Unlock()
			if ch != nil {
				ch <- f
			}
			continue
		}
		if !registered && f.Action != "/register" {
			s.send(c, f.Error(409, "not registered"))
			continue
		}
		switch f.Action {
		case "/register":
			if code, msg := s.checkRegister(f, module, instance); code != 0 {
				s.send(c, f.Error(code, msg))
				s.close(c, 1008, msg)
				return
			}
			var reg Registration
			_ = json.Unmarshal(f.Payload, &reg)
			s.mu.Lock()
			if old := s.conn; old != nil && old != c && !old.closed {
				go s.close(old, 1001, "replaced by new registration")
			}
			s.conn = c
			c.instance = instance
			c.lastBeat = time.Now()
			s.registrations = append(s.registrations, reg)
			s.changed.Broadcast()
			s.mu.Unlock()
			registered = true
			s.send(c, f.Result(map[string]any{"heartbeat_interval": s.opts.HeartbeatInterval, "instance_id": instance, "gateway_worker": 4242, "suspect_after": s.opts.SuspectAfter, "offline_after": s.opts.OfflineAfter}))
			go s.watchHeartbeat(ctx, c)
		case "/heartbeat":
			var hb Heartbeat
			_ = json.Unmarshal(f.Payload, &hb)
			s.mu.Lock()
			c.lastBeat = time.Now()
			s.heartbeats = append(s.heartbeats, hb)
			silent := s.silent
			s.changed.Broadcast()
			s.mu.Unlock()
			if !silent {
				s.send(c, f.Result(map[string]any{"state": "ready"}))
			}
		case "/status":
			var st Status
			_ = json.Unmarshal(f.Payload, &st)
			if st.State != "ready" && st.State != "draining" && st.State != "overloaded" {
				s.send(c, f.Error(422, "state"))
				continue
			}
			s.mu.Lock()
			s.statuses = append(s.statuses, st)
			s.changed.Broadcast()
			s.mu.Unlock()
			s.send(c, f.Result(nil))
		case "/unregister":
			var u Unregister
			_ = json.Unmarshal(f.Payload, &u)
			s.mu.Lock()
			s.unregisters = append(s.unregisters, u)
			s.changed.Broadcast()
			s.mu.Unlock()
			s.send(c, f.Result(nil))
			// the module closes 1000 itself; we record what it sends
		default:
			s.send(c, f.Error(404, "unknown action"))
		}
	}
}

func (s *Stub) checkRegister(f frame.Frame, module, instance string) (int, string) {
	s.mu.Lock()
	fc, fm := s.failCode, s.failMsg
	s.mu.Unlock()
	if fc != 0 {
		return fc, fm
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(f.Payload, &raw); err != nil {
		return 400, "p is not an object"
	}
	for _, k := range []string{"module", "instance", "version", "address", "prefixes", "capacity"} {
		if _, ok := raw[k]; !ok {
			return 400, "missing " + k
		}
	}
	var reg Registration
	if err := json.Unmarshal(f.Payload, &reg); err != nil {
		return 400, err.Error()
	}
	if reg.Module != module || reg.Instance != instance {
		return 422, "module/instance differ from URL"
	}
	host, port, err := net.SplitHostPort(reg.Address)
	if err != nil || net.ParseIP(host) == nil || net.ParseIP(host).To4() == nil {
		return 400, "address must be ipv4:port"
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return 400, "address port"
	}
	if !net.ParseIP(host).IsPrivate() && !net.ParseIP(host).IsLoopback() {
		return 403, "address outside allowed_cidr"
	}
	if len(reg.Prefixes) == 0 {
		return 400, "prefixes empty"
	}
	for i, p := range reg.Prefixes {
		if !prefix.MatchString(p) {
			return 400, "prefix " + p
		}
		for _, q := range reg.Prefixes[:i] {
			if strings.HasPrefix(p+"/", q+"/") || strings.HasPrefix(q+"/", p+"/") {
				return 422, "prefixes overlap"
			}
		}
	}
	if reg.Capacity < 1 {
		return 422, "capacity"
	}
	return 0, ""
}

func (s *Stub) watchHeartbeat(ctx context.Context, c *conn) {
	interval := time.Duration(s.opts.HeartbeatInterval) * time.Second
	tick := time.NewTicker(interval / 4)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.mu.Lock()
			late := time.Since(c.lastBeat)
			closed := c.closed
			s.mu.Unlock()
			if closed {
				return
			}
			if late >= time.Duration(s.opts.OfflineAfter)*interval {
				s.close(c, 4000, "heartbeat timeout")
				return
			}
		}
	}
}

func (s *Stub) send(c *conn, f frame.Frame) {
	b, err := f.Encode()
	if err != nil {
		s.t.Errorf("stub encode: %v", err)
		return
	}
	_ = c.ws.Write(context.Background(), websocket.MessageText, b)
}

func (s *Stub) close(c *conn, code int, reason string) {
	s.mu.Lock()
	if c.closed {
		s.mu.Unlock()
		return
	}
	c.closed = true
	s.closeCodes = append(s.closeCodes, code)
	s.changed.Broadcast()
	s.mu.Unlock()
	_ = c.ws.Close(websocket.StatusCode(code), reason)
}

func (s *Stub) observeClose(c *conn, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if code := websocket.CloseStatus(err); code != -1 {
		s.closeCodes = append(s.closeCodes, int(code))
	} else {
		s.closeCodes = append(s.closeCodes, 1006)
	}
	s.changed.Broadcast()
}

// ── controls ────────────────────────────────────────────────────────────

// FailRegister makes every /register answer CALLERROR code (0 = normal).
func (s *Stub) FailRegister(code int, msg string) {
	s.mu.Lock()
	s.failCode, s.failMsg = code, msg
	s.mu.Unlock()
}

// SilenceHeartbeats stops answering /heartbeat (the "gateway hung" case).
func (s *Stub) SilenceHeartbeats(on bool) {
	s.mu.Lock()
	s.silent = on
	s.mu.Unlock()
}

// DropConnection kills the current socket without a close frame.
func (s *Stub) DropConnection() {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		_ = c.ws.CloseNow()
	}
}

// Replace closes the current socket 1001 as a newer registration of the same
// instance would.
func (s *Stub) Replace() {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		s.close(c, 1001, "replaced by new registration")
	}
}

// SendRaw writes bytes to the module as one text frame (malformed input).
func (s *Stub) SendRaw(t testing.TB, b []byte) {
	t.Helper()
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c == nil {
		t.Fatal("stub: no connection")
	}
	_ = c.ws.Write(context.Background(), websocket.MessageText, b)
}

// Kick closes the current socket with the given code, as the gateway would
// on heartbeat timeout (4000) or an internal error (1011).
func (s *Stub) Kick(code int, reason string) {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		s.close(c, code, reason)
	}
}

// Call sends a CALL to the module and waits for its answer.
func (s *Stub) Call(t testing.TB, action string, payload any, timeout time.Duration) frame.Frame {
	t.Helper()
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c == nil {
		t.Fatal("stub: no connection")
	}
	f := frame.NewCall(action, payload)
	ch := make(chan frame.Frame, 1)
	s.mu.Lock()
	c.pending[f.ID] = ch
	s.mu.Unlock()
	s.send(c, f)
	select {
	case r := <-ch:
		return r
	case <-time.After(timeout):
		t.Fatalf("stub: no answer to %s in %v", action, timeout)
		return frame.Frame{}
	}
}

// ── observations ────────────────────────────────────────────────────────

func (s *Stub) wait(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() { s.mu.Lock(); s.changed.Broadcast(); s.mu.Unlock() })
	defer timer.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("stub: timeout waiting for %s", what)
		}
		s.changed.Wait()
	}
}

// WaitRegistered blocks until at least one /register was accepted.
func (s *Stub) WaitRegistered(t testing.TB, timeout time.Duration) Registration {
	t.Helper()
	s.wait(t, timeout, "registration", func() bool { return len(s.registrations) >= 1 })
	return s.registrations[len(s.registrations)-1]
}

// WaitRegistrations blocks until n registrations were accepted.
func (s *Stub) WaitRegistrations(t testing.TB, n int, timeout time.Duration) int {
	t.Helper()
	s.wait(t, timeout, fmt.Sprintf("%d registrations", n), func() bool { return len(s.registrations) >= n })
	return len(s.registrations)
}

// Registrations counts accepted /register frames.
func (s *Stub) Registrations() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.registrations) }

// LastRegistration returns the most recent accepted registration.
func (s *Stub) LastRegistration() Registration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registrations[len(s.registrations)-1]
}

// WaitHeartbeats blocks until n heartbeats arrived.
func (s *Stub) WaitHeartbeats(t testing.TB, n int, timeout time.Duration) int {
	t.Helper()
	s.wait(t, timeout, fmt.Sprintf("%d heartbeats", n), func() bool { return len(s.heartbeats) >= n })
	return len(s.heartbeats)
}

// LastHeartbeat returns the most recent heartbeat payload.
func (s *Stub) LastHeartbeat() Heartbeat {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heartbeats[len(s.heartbeats)-1]
}

// WaitStatus blocks until a /status with the given state arrived.
func (s *Stub) WaitStatus(t testing.TB, state string, timeout time.Duration) Status {
	t.Helper()
	var found Status
	s.wait(t, timeout, "status "+state, func() bool {
		for _, st := range s.statuses {
			if st.State == state {
				found = st
				return true
			}
		}
		return false
	})
	return found
}

// WaitUnregister blocks until /unregister arrived.
func (s *Stub) WaitUnregister(t testing.TB, timeout time.Duration) Unregister {
	t.Helper()
	s.wait(t, timeout, "unregister", func() bool { return len(s.unregisters) >= 1 })
	return s.unregisters[len(s.unregisters)-1]
}

// CloseCodes lists the close code of every finished connection, in order —
// the code the stub sent, or the code it received from the module (1006 when
// the socket died without a close frame).
func (s *Stub) CloseCodes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int(nil), s.closeCodes...)
}

// WaitCloseCodes blocks until n connections have finished.
func (s *Stub) WaitCloseCodes(t testing.TB, n int, timeout time.Duration) []int {
	t.Helper()
	s.wait(t, timeout, fmt.Sprintf("%d closes", n), func() bool { return len(s.closeCodes) >= n })
	return append([]int(nil), s.closeCodes...)
}

// HandshakeRejections counts 401s on the handshake.
func (s *Stub) HandshakeRejections() int { s.mu.Lock(); defer s.mu.Unlock(); return s.rejections }
