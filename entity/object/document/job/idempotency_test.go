package job

import (
	"bytes"
	"context"
	"testing"

	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/jackc/pgx/v5"
)

// replayDoer counts how many times the database was reached.
type replayDoer struct{ calls int }

func (r *replayDoer) Do(ctx context.Context, s pgtx.Session, req *pgtx.Request, fn func(context.Context, pgx.Tx) error) error {
	r.calls++
	return errFakeCreated
}

// Idempotency-Key replay is rest.Writable.Create's, wired like the rest of
// the form: same body replays the stored answer without a second call.
func TestIdempotencyKey_ReplaysStoredResponse(t *testing.T) {
	d := &replayDoer{}
	h := srv(t, New(Config{Doer: d}))
	hdr := []string{"Idempotency-Key", "k1"}
	body := `{"code":"j1","scheduler":"7f3a0000-0000-4000-8000-000000000001","program":"7f3a0000-0000-4000-8000-000000000002"}`
	first := do(h, "POST", Prefix, body, hdr...)
	second := do(h, "POST", Prefix, body, hdr...)
	if d.calls != 1 {
		t.Fatalf("db calls %d, want 1", d.calls)
	}
	if first.Code != second.Code || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatalf("replay differs: %d/%d %s/%s", first.Code, second.Code, first.Body, second.Body)
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("replay must be marked")
	}
}

type statusDoer struct {
	calls int
	err   error
}

func (d *statusDoer) Do(ctx context.Context, s pgtx.Session, req *pgtx.Request, fn func(context.Context, pgx.Tx) error) error {
	d.calls++
	return d.err
}

// A 5xx is not stored: the next request with the same key retries.
func TestIdempotencyKey_5xxIsNotStored(t *testing.T) {
	d := &statusDoer{err: problem.New(500, "internal", "Internal error", "deadlock")}
	h := srv(t, New(Config{Doer: d}))
	hdr := []string{"Idempotency-Key", "k5"}
	body := `{"code":"j1","scheduler":"7f3a0000-0000-4000-8000-000000000001","program":"7f3a0000-0000-4000-8000-000000000002"}`
	first := do(h, "POST", Prefix, body, hdr...)
	second := do(h, "POST", Prefix, body, hdr...)
	if first.Code != 500 || second.Code != 500 || d.calls != 2 {
		t.Fatalf("%d/%d calls=%d — a 5xx must be retried, not replayed", first.Code, second.Code, d.calls)
	}
	if second.Header().Get("Idempotency-Replayed") != "" {
		t.Fatal("5xx replayed")
	}
}
