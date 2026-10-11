// Package gatewayclient is the module's side of the gateway control plane
// (module-GatewayAPI, README section "Control plane"): connect, /register, heartbeat,
// /status, /unregister; answers /ping, /drain, /reload; reconnects with backoff
// 1→30 s; drains on request (SIGTERM is the caller's signal to call Drain).
package gatewayclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

// State is the instance state as the module sees it.
type State string

const (
	Offline    State = "offline"
	Connecting State = "connecting"
	Ready      State = "ready"
	Overloaded State = "overloaded"
	Draining   State = "draining"
)

// Event is a lifecycle notification for logs and tests.
type Event string

const (
	EventConnectFailed     Event = "connect_failed"     // dial failed; backoff
	EventHandshakeRejected Event = "handshake_rejected" // HTTP 401/403 before upgrade; slow retry with a fresh token
	EventRefused           Event = "refused"            // CALLERROR on /register (400/403/409/422); slow retry
	EventRegistered        Event = "registered"         // ready
	EventDisconnected      Event = "disconnected"       // socket lost or silent gateway; backoff
	EventReplaced          Event = "replaced"           // close 1001 (replaced or gateway going away); backoff
	EventDrained           Event = "drained"            // /unregister done, socket closed 1000; Run returns nil
)

// ErrReplaced was returned by Run after close 1001.
//
// Deprecated: Run reconnects after 1001 (contract K2) and never returns it;
// kept so that code written against v0.1.0 still compiles.
var ErrReplaced = errors.New("gatewayclient: replaced by a newer registration")

// errGoingAway is a close 1001: a newer registration of this instance, or a
// gateway process going away (a worker stopping, a pod rolling). Both end
// the same way — reconnect.
var errGoingAway = errors.New("closed 1001 by the gateway")

var errDrained = errors.New("drained")

// Registration are the parameters a /reload may change.
type Registration struct {
	Address  string
	Prefixes []string
	Capacity int
}

// Config describes the module to the gateway. Everything but the hooks comes
// from the environment.
type Config struct {
	URL      string // ws://host:port/gateway
	Module   string
	Instance string
	Version  string
	Build    string
	// Address is host:port of the data plane, an IPv4 literal. Empty: the
	// local address of the control socket plus ListenPort.
	Address    string
	ListenPort int
	Prefixes   []string
	Capacity   int

	// Token returns the service token the gateway accepts; called on every
	// (re)connection, so it may refresh.
	Token func(ctx context.Context) (string, error)
	// InFlight reports requests currently being served (heartbeat, /ping,
	// drain). Nil: the client's Load, if one is taken, else zero.
	InFlight func() int
	// OnReload runs on /reload; a non-nil result with changed prefixes,
	// address or capacity makes the client re-register.
	OnReload func() Registration
	OnEvent  func(Event)
	Logger   *slog.Logger

	// Reconnect gives the wait before reconnection attempt n (0-based) after
	// a lost socket or a failed dial; default DefaultReconnect (1→30 s, ±20 %).
	Reconnect func(attempt int) time.Duration
	// ReconnectAfterRefusal is the wait after a registration refusal or a
	// rejected handshake; default 30 s.
	ReconnectAfterRefusal func() time.Duration
	// DrainDeadline bounds the wait for in-flight requests; default 30 s.
	DrainDeadline time.Duration
	// HTTPClient performs the handshake; default captures the local address.
	HTTPClient *http.Client
}

// Client is one control-plane connection, kept alive by Run.
type Client struct {
	cfg Config
	log *slog.Logger

	mu         sync.Mutex
	state      State
	reg        Registration
	overloaded bool
	active     *session             // the live connection, nil between sessions
	load       atomic.Pointer[Load] // counts in flight when Config.InFlight is nil
	loadOnce   sync.Once
	capacity   atomic.Int32 // = reg.Capacity, read on the request path by Load
	// statusMu serialises every /status, held across the call: what the
	// gateway last heard is then session.said, and nothing follows draining
	statusMu sync.Mutex
	drainCh  chan drainReq
	drained  chan struct{}
}

type drainReq struct {
	reason   string
	deadline time.Duration
}

var (
	segment = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	prefix  = regexp.MustCompile(`^/api/v2/[a-z0-9_/-]+$`)
)

// New validates the configuration.
func New(cfg Config) (*Client, error) {
	switch {
	case cfg.URL == "":
		return nil, errors.New("gatewayclient: URL required")
	case !segment.MatchString(cfg.Module), !segment.MatchString(cfg.Instance):
		return nil, errors.New("gatewayclient: module/instance must match ^[a-z0-9][a-z0-9_-]{0,62}$")
	case cfg.Version == "":
		return nil, errors.New("gatewayclient: version required")
	case len(cfg.Prefixes) == 0:
		return nil, errors.New("gatewayclient: at least one prefix")
	case cfg.Capacity < 1:
		return nil, errors.New("gatewayclient: capacity ≥ 1")
	case cfg.Token == nil:
		return nil, errors.New("gatewayclient: Token required")
	case cfg.Address == "" && cfg.ListenPort == 0:
		return nil, errors.New("gatewayclient: Address or ListenPort required")
	case cfg.Address == "" && cfg.HTTPClient != nil:
		return nil, errors.New("gatewayclient: a custom HTTPClient cannot capture the local address; set Address")
	}
	for _, p := range cfg.Prefixes {
		if !prefix.MatchString(p) {
			return nil, fmt.Errorf("gatewayclient: bad prefix %q", p)
		}
	}
	if cfg.Reconnect == nil {
		cfg.Reconnect = DefaultReconnect
	}
	if cfg.ReconnectAfterRefusal == nil {
		cfg.ReconnectAfterRefusal = func() time.Duration { return 30 * time.Second }
	}
	if cfg.DrainDeadline == 0 {
		cfg.DrainDeadline = 30 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	c := &Client{
		cfg:     cfg,
		log:     cfg.Logger.With("module", cfg.Module, "instance", cfg.Instance),
		state:   Offline,
		reg:     Registration{Address: cfg.Address, Prefixes: cfg.Prefixes, Capacity: cfg.Capacity},
		drainCh: make(chan drainReq, 1),
		drained: make(chan struct{}),
	}
	c.capacity.Store(int32(cfg.Capacity))
	return c, nil
}

// DefaultReconnect is the protocol's schedule: 1, 2, 4, 8, 16, 30, 30… s, ±20 %.
func DefaultReconnect(attempt int) time.Duration {
	base := time.Duration(1<<min(attempt, 5)) * time.Second
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	jitter := 1 + (rand.Float64()*0.4 - 0.2)
	return time.Duration(float64(base) * jitter)
}

// inFlight is what heartbeat, /ping and drain report.
func (c *Client) inFlight() int {
	if c.cfg.InFlight != nil {
		return c.cfg.InFlight()
	}
	if l := c.load.Load(); l != nil {
		return l.Value()
	}
	return 0
}

// State returns the current instance state.
func (c *Client) State() State { c.mu.Lock(); defer c.mu.Unlock(); return c.state }

func (c *Client) setState(s State) { c.mu.Lock(); c.state = s; c.mu.Unlock() }

func (c *Client) emit(e Event) {
	if c.cfg.OnEvent != nil {
		c.cfg.OnEvent(e)
	}
}

// Run keeps the control connection alive until ctx ends or Drain completes
// (returns nil).
func (c *Client) Run(ctx context.Context) error {
	c.mu.Lock()
	select {
	case <-c.drained: // a previous Run drained; start a fresh cycle
		c.drained = make(chan struct{})
	default:
	}
	c.mu.Unlock()
	attempt := 0
	replaced := 0 // 1001s in a row, each soon after its registration
	for {
		began := time.Now()
		registered, err := c.runSession(ctx)
		if registered {
			attempt = 0
		}
		var wait time.Duration
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, errDrained):
			c.setState(Offline)
			c.emit(EventDrained)
			return nil
		case errors.Is(err, errGoingAway):
			// two instances under one name replace each other this way in a
			// loop; the gateway's log shows it as frequent "replaced" — a
			// configuration error of the module (K2), not a reason to stop,
			// but the loop slows down as 1→30 s instead of turning every second
			if time.Since(began) < 30*time.Second {
				replaced++
			} else {
				replaced = 0
			}
			c.setState(Offline)
			c.log.Warn("gateway closed the socket 1001; reconnecting", "in_a_row", replaced)
			c.emit(EventReplaced)
			wait = c.cfg.Reconnect(max(attempt, replaced-1))
			attempt++
		case errors.As(err, new(*refusal)):
			c.setState(Offline)
			var r *refusal
			errors.As(err, &r)
			c.log.Error("gateway refused", "code", r.code, "reason", r.msg, "handshake", r.handshake)
			if r.handshake {
				c.emit(EventHandshakeRejected)
			} else {
				c.emit(EventRefused)
			}
			wait = c.cfg.ReconnectAfterRefusal()
		case errors.Is(err, errDial):
			c.setState(Offline)
			c.emit(EventConnectFailed)
			wait = c.cfg.Reconnect(attempt)
			attempt++
		default:
			c.setState(Offline)
			c.log.Warn("gateway connection lost", "err", err)
			c.emit(EventDisconnected)
			wait = c.cfg.Reconnect(attempt)
			attempt++
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Drain announces draining, waits for in-flight requests (bounded by
// DrainDeadline), unregisters and closes; Run then returns nil.
func (c *Client) Drain(ctx context.Context, reason string) error {
	return c.drain(ctx, reason, c.cfg.DrainDeadline)
}

func (c *Client) drain(ctx context.Context, reason string, deadline time.Duration) error {
	c.mu.Lock()
	drained := c.drained
	c.mu.Unlock()
	select {
	case c.drainCh <- drainReq{reason: reason, deadline: deadline}:
	default:
		// a drain is already pending
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetOverloaded announces overloaded (true) or ready (false); between
// connections it is kept and told at the next registration. Nothing is sent
// while draining: draining → ready is not a transition the gateway accepts.
func (c *Client) SetOverloaded(on bool) {
	c.mu.Lock()
	c.overloaded = on
	s := c.active
	c.mu.Unlock()
	if s != nil {
		c.syncStatus(s)
	}
}

// syncStatus tells the gateway, on s, the overloaded flag as it is when the
// turn comes — not as it was when asked, so two callers racing each other
// (a Load watcher and a fresh registration) end on the current value.
func (c *Client) syncStatus(s *session) {
	c.statusMu.Lock()
	defer c.statusMu.Unlock()
	c.mu.Lock()
	want, current := "ready", c.active == s
	if c.overloaded {
		want = "overloaded"
	}
	c.mu.Unlock()
	s.mu.Lock()
	draining := s.draining
	s.mu.Unlock()
	if !current || draining || s.said == want {
		return
	}
	s.status(want, "")
	s.said = want
	c.mu.Lock()
	if c.active == s { // the socket may have ended during the call
		c.state = Ready
		if want == "overloaded" {
			c.state = Overloaded
		}
	}
	c.mu.Unlock()
}

type refusal struct {
	code      int
	msg       string
	handshake bool
}

func (r *refusal) Error() string { return fmt.Sprintf("refused %d: %s", r.code, r.msg) }

var errDial = errors.New("dial")
