package gatewayclient

import (
	"sync"
	"testing"
	"time"
)

// Recorder collects events for tests (OnEvent: rec.Record).
type Recorder struct {
	mu     sync.Mutex
	cond   *sync.Cond
	events []Event
}

// NewRecorder returns an empty recorder.
func NewRecorder() *Recorder {
	r := &Recorder{}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// Record appends an event.
func (r *Recorder) Record(e Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.cond.Broadcast()
	r.mu.Unlock()
}

// Events returns a copy of what was recorded.
func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.events...)
}

// Wait blocks until e was recorded at least once.
func (r *Recorder) Wait(t testing.TB, e Event, timeout time.Duration) {
	t.Helper()
	r.WaitN(t, e, 1, timeout)
}

// WaitN blocks until e was recorded at least n times.
func (r *Recorder) WaitN(t testing.TB, e Event, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	timer := time.AfterFunc(timeout, func() { r.mu.Lock(); r.cond.Broadcast(); r.mu.Unlock() })
	defer timer.Stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		count := 0
		for _, x := range r.events {
			if x == e {
				count++
			}
		}
		if count >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorder: %s ×%d not seen in %v; got %v", e, n, timeout, r.events)
		}
		r.cond.Wait()
	}
}
