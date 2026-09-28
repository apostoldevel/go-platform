package gatewayclient

import "sync/atomic"

// Load counts the requests in flight and keeps the instance's overloaded
// state in step with the registered capacity: overloaded when the count
// reaches it, ready again when the count falls to three quarters of it and at
// least one below (the gap keeps a count that hovers at the capacity from
// sending a /status per request; at capacity 1 there is no room for a gap).
// It satisfies platform.Config.InFlight.
//
// Add never waits for the gateway: when the count reaches either mark it
// wakes one goroutine that compares the current count with the current
// capacity and calls SetOverloaded, so the answer to a /status is never on a
// request's path, and the last change is always judged after the last Add.
// Do not call SetOverloaded yourself while a Load is in use — the two would
// overwrite each other.
type Load struct {
	c    *Client
	n    atomic.Int32
	wake chan struct{}
}

// Load returns the client's counter, the same one on every call. The client
// reports it at heartbeat, /ping and drain unless Config.InFlight was given.
// Its goroutine lives as long as the client: one per process.
func (c *Client) Load() *Load {
	c.loadOnce.Do(func() {
		l := &Load{c: c, wake: make(chan struct{}, 1)}
		c.load.Store(l)
		go l.watch()
	})
	return c.load.Load()
}

// Add changes the count by d and returns the new count.
func (l *Load) Add(d int32) int32 {
	n := l.n.Add(d)
	if capacity := l.c.capacity.Load(); n >= capacity || n <= low(capacity) {
		l.poke()
	}
	return n
}

// Value is the current count.
func (l *Load) Value() int { return int(l.n.Load()) }

func (l *Load) poke() {
	select {
	case l.wake <- struct{}{}:
	default: // a check is already pending; it reads the count after this Add
	}
}

// low is the count at which an overloaded instance is ready again.
func low(capacity int32) int32 { return capacity - max(1, capacity/4) }

func (l *Load) watch() {
	on := false
	for range l.wake {
		capacity, n := l.c.capacity.Load(), l.n.Load()
		switch {
		case !on && n >= capacity:
			on = true
		case on && n <= low(capacity):
			on = false
		default:
			continue
		}
		l.c.SetOverloaded(on)
	}
}
