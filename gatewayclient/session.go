package gatewayclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/apostoldevel/go-platform/gateway/frame"
	"github.com/coder/websocket"
)

// session is one socket lifetime: dial → /register → heartbeat/dispatch → close.
type session struct {
	c        *Client
	ws       *websocket.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	interval time.Duration

	mu       sync.Mutex
	pending  map[string]chan frame.Frame
	closeErr error // set by the reader when the socket ends
	draining bool
	done     chan struct{} // closed when the reader stops
}

func (c *Client) runSession(ctx context.Context) (registered bool, err error) {
	c.setState(Connecting)
	token, err := c.cfg.Token(ctx)
	if err != nil {
		return false, fmt.Errorf("%w: token: %v", errDial, err)
	}
	c.mu.Lock()
	reg := c.reg
	c.mu.Unlock()

	var local net.Addr
	httpClient := c.cfg.HTTPClient
	if httpClient == nil {
		d := &net.Dialer{Timeout: 5 * time.Second}
		httpClient = &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				conn, err := d.DialContext(ctx, network, addr)
				if err == nil {
					local = conn.LocalAddr()
				}
				return conn, err
			},
		}}
	}
	url := fmt.Sprintf("%s/%s/%s", c.cfg.URL, c.cfg.Module, c.cfg.Instance)
	dialCtx, cancelDial := context.WithTimeout(ctx, 10*time.Second)
	defer cancelDial()
	ws, resp, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		HTTPClient: httpClient,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		if resp != nil && resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return false, &refusal{code: resp.StatusCode, msg: resp.Status, handshake: true}
		}
		return false, fmt.Errorf("%w: %v", errDial, err)
	}
	ws.SetReadLimit(frame.MaxSize + 1024)
	sctx, cancel := context.WithCancel(ctx)
	s := &session{c: c, ws: ws, ctx: sctx, cancel: cancel, pending: map[string]chan frame.Frame{}, done: make(chan struct{})}
	defer s.cancel()
	go s.reader()

	if reg.Address == "" {
		host := "127.0.0.1"
		if tcp, ok := local.(*net.TCPAddr); ok && tcp.IP.To4() != nil {
			host = tcp.IP.String()
		}
		reg.Address = net.JoinHostPort(host, strconv.Itoa(c.cfg.ListenPort))
	}

	res, err := s.call("/register", map[string]any{
		"module": c.cfg.Module, "instance": c.cfg.Instance, "version": c.cfg.Version, "build": c.cfg.Build,
		"address": reg.Address, "prefixes": reg.Prefixes, "capacity": reg.Capacity,
	}, 5*time.Second)
	if err != nil {
		return false, s.endError(err)
	}
	if res.Type == frame.CallError {
		s.close(websocket.StatusPolicyViolation, "refused")
		return false, &refusal{code: res.Code, msg: res.Message}
	}
	var ack struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if err := json.Unmarshal(res.Payload, &ack); err != nil || ack.HeartbeatInterval < 1 {
		s.close(websocket.StatusProtocolError, "bad /register result")
		return false, errors.New("bad /register result")
	}
	s.interval = time.Duration(ack.HeartbeatInterval) * time.Second

	c.mu.Lock()
	c.active = s
	c.reg = reg
	overloaded := c.overloaded
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.active = nil; c.mu.Unlock() }()
	c.setState(Ready)
	c.emit(EventRegistered)
	c.log.Info("registered", "address", reg.Address, "prefixes", reg.Prefixes, "heartbeat_interval", ack.HeartbeatInterval)
	if overloaded {
		s.status("overloaded", "")
	}
	return true, s.loop()
}

// loop drives heartbeats and drain until the socket ends.
func (s *session) loop() error {
	c := s.c
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	unanswered := 0
	for {
		select {
		case <-s.done:
			return s.endError(nil)
		case req := <-c.drainCh:
			return s.drain(req)
		case <-ticker.C:
			res, err := s.call("/heartbeat", map[string]any{"in_flight": c.cfg.InFlight()}, s.interval)
			switch {
			case err != nil && errors.Is(err, errTimeout):
				unanswered++
				if unanswered >= 2 {
					c.log.Warn("gateway silent: two heartbeats unanswered")
					s.close(closeSilentGateway, "heartbeat unanswered")
					return errors.New("gateway silent")
				}
			case err != nil:
				return s.endError(err)
			default:
				unanswered = 0
				var st struct{ State string }
				_ = json.Unmarshal(res.Payload, &st)
				if st.State == "offline" {
					s.close(closeSilentGateway, "offline per gateway")
					return errors.New("gateway reports offline")
				}
			}
		}
	}
}

// drain runs the K4/K7 shutdown: /status draining → wait → /unregister → close 1000.
func (s *session) drain(req drainReq) error {
	c := s.c
	c.setState(Draining)
	s.mu.Lock()
	s.draining = true
	s.mu.Unlock()
	s.status("draining", req.reason)
	deadline := time.After(req.deadline)
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
wait:
	for c.cfg.InFlight() > 0 {
		select {
		case <-deadline:
			c.log.Warn("drain deadline reached with requests in flight", "in_flight", c.cfg.InFlight())
			break wait
		case <-s.done:
			break wait
		case <-poll.C:
		}
	}
	_, _ = s.call("/unregister", map[string]any{"reason": req.reason}, 2*time.Second)
	s.close(websocket.StatusNormalClosure, "unregistered")
	c.mu.Lock()
	close(c.drained)
	c.mu.Unlock()
	return errDrained
}

func (s *session) status(state, reason string) {
	p := map[string]any{"state": state}
	if reason != "" {
		p["reason"] = reason
	}
	_, _ = s.call("/status", p, 5*time.Second)
}

var errTimeout = errors.New("no answer")

// closeSilentGateway is the module-side close code for "the gateway stopped
// answering" — 4001, so that 1001 keeps its contract meaning (replaced).
const closeSilentGateway = websocket.StatusCode(4001)

// call sends a CALL and waits for its CALLRESULT/CALLERROR.
func (s *session) call(action string, payload any, timeout time.Duration) (frame.Frame, error) {
	f := frame.NewCall(action, payload)
	ch := make(chan frame.Frame, 1)
	s.mu.Lock()
	s.pending[f.ID] = ch
	s.mu.Unlock()
	if err := s.send(f); err != nil {
		return frame.Frame{}, err
	}
	select {
	case r := <-ch:
		return r, nil
	case <-s.done:
		return frame.Frame{}, s.endError(nil)
	case <-time.After(timeout):
		s.mu.Lock()
		delete(s.pending, f.ID)
		s.mu.Unlock()
		return frame.Frame{}, fmt.Errorf("%w to %s within %v", errTimeout, action, timeout)
	}
}

func (s *session) send(f frame.Frame) error {
	b, err := f.Encode()
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	return s.ws.Write(wctx, websocket.MessageText, b)
}

// reader dispatches inbound frames: answers to pending calls, commands to handlers.
func (s *session) reader() {
	defer close(s.done)
	for {
		typ, data, err := s.ws.Read(s.ctx)
		if err != nil {
			s.mu.Lock()
			s.closeErr = err
			s.mu.Unlock()
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		f, err := frame.Parse(data)
		if errors.Is(err, frame.ErrTooLarge) { // K1: > 64 KiB → close 1009
			s.close(websocket.StatusMessageTooBig, "frame too large")
			s.mu.Lock()
			s.closeErr = errors.New("frame too large")
			s.mu.Unlock()
			return
		}
		if err != nil {
			_ = s.send(frame.Frame{Type: frame.CallError, ID: frame.NewID(), Code: 400, Message: err.Error()})
			continue
		}
		switch f.Type {
		case frame.CallResult, frame.CallError:
			s.mu.Lock()
			ch := s.pending[f.ID]
			delete(s.pending, f.ID)
			s.mu.Unlock()
			if ch != nil {
				ch <- f
			}
		case frame.Call:
			go s.handle(f)
		}
	}
}

// handle answers the gateway's commands (contract K5).
func (s *session) handle(f frame.Frame) {
	c := s.c
	switch f.Action {
	case "/ping":
		_ = s.send(f.Result(map[string]any{"in_flight": c.cfg.InFlight()}))
	case "/drain":
		var p struct {
			Reason   string `json:"reason"`
			Deadline int    `json:"deadline"`
		}
		_ = json.Unmarshal(f.Payload, &p)
		if p.Reason == "" {
			p.Reason = "drain"
		}
		deadline := c.cfg.DrainDeadline
		if p.Deadline > 0 {
			deadline = time.Duration(p.Deadline) * time.Second
		}
		_ = s.send(f.Result(nil))
		go func() { _ = c.drain(context.Background(), p.Reason, deadline) }()
	case "/reload":
		_ = s.send(f.Result(nil))
		if c.cfg.OnReload == nil {
			return
		}
		next := c.cfg.OnReload()
		c.mu.Lock()
		cur := c.reg
		changed := next.Address != "" && next.Address != cur.Address ||
			next.Capacity != 0 && next.Capacity != cur.Capacity ||
			len(next.Prefixes) > 0 && !equal(next.Prefixes, cur.Prefixes)
		if changed {
			if next.Address != "" {
				cur.Address = next.Address
			}
			if next.Capacity != 0 {
				cur.Capacity = next.Capacity
			}
			if len(next.Prefixes) > 0 {
				cur.Prefixes = next.Prefixes
			}
			c.reg = cur
		}
		c.mu.Unlock()
		if changed {
			c.log.Info("registration parameters changed on /reload; re-registering")
			s.close(websocket.StatusServiceRestart, "re-register")
		}
	default:
		_ = s.send(f.Error(404, "unknown action"))
	}
}

func (s *session) close(code websocket.StatusCode, reason string) {
	_ = s.ws.Close(code, reason)
	s.cancel()
}

// endError translates how the socket ended into Run's vocabulary.
func (s *session) endError(err error) error {
	<-s.done
	s.mu.Lock()
	closeErr, draining := s.closeErr, s.draining
	s.mu.Unlock()
	if draining {
		return errDrained
	}
	switch websocket.CloseStatus(closeErr) {
	case websocket.StatusGoingAway: // 1001 — replaced
		return ErrReplaced
	case websocket.StatusPolicyViolation: // 1008 — refusal without CALLERROR
		// K2 amendment (T266): until libapostol can refuse before 101, the
		// gateway closes 1008 right after the upgrade with the reason.
		switch r := reason(closeErr); r {
		case "unauthorized", "forbidden", "not-found", "bad-request":
			return &refusal{code: 1008, msg: r, handshake: true}
		default:
			return &refusal{code: 1008, msg: r}
		}
	}
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return errors.New("socket closed")
}

func reason(err error) string {
	var ce websocket.CloseError
	if errors.As(err, &ce) {
		return ce.Reason
	}
	return ""
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
