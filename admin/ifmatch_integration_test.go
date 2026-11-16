//go:build integration

package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/jackc/pgx/v5"
)

// raceDoer makes two requests meet at the write: each one stops just before
// the call of the write function, after it has read the row and passed the
// If-Match comparison. The first to arrive waits until the second has
// arrived; the second waits until the first request has committed (its Do has
// returned). The order is fixed by channels, never by a sleep.
type raceDoer struct {
	resttest.Doer
	fn       string
	arrivals atomic.Int32
	second   chan struct{} // closed when the second request is at its write
	done     chan struct{} // closed when the first request has committed
	t        *testing.T
}

func (d *raceDoer) Do(ctx context.Context, s pgtx.Session, req *pgtx.Request, fn func(context.Context, pgx.Tx) error) error {
	var first bool
	err := d.Doer.Do(ctx, s, req, func(ctx context.Context, tx pgx.Tx) error {
		return fn(ctx, raceTx{Tx: tx, d: d, first: &first})
	})
	if first {
		close(d.done)
	}
	return err
}

type raceTx struct {
	pgx.Tx
	d     *raceDoer
	first *bool
}

func (x raceTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, x.d.fn) || (len(args) > 0 && args[0] == x.d.fn) {
		if x.d.arrivals.Add(1) == 1 {
			*x.first = true
			select {
			case <-x.d.second:
			case <-time.After(20 * time.Second):
				x.d.t.Error("the second request never reached its write")
			}
		} else {
			close(x.d.second)
			select {
			case <-x.d.done:
			case <-time.After(20 * time.Second):
				x.d.t.Error("the first request never committed")
			}
		}
	}
	return x.Tx.Query(ctx, sql, args...)
}

// Two PATCHes of the same user that carry the same ETag: the first commits,
// the second must be 412 and the row must hold the first one's value. Without
// REPEATABLE READ the second write runs on the new row version and replaces
// the first (a lost update, answered 200).
func TestIntegration_IfMatchWritesDoNotLoseUpdates(t *testing.T) {
	race := &raceDoer{fn: "set_user_profile", second: make(chan struct{}), done: make(chan struct{}), t: t}
	l := resttest.Start(t, "go-admin-race", func(r resttest.Doer) platform.Module {
		race.Doer = r
		return New(Config{Doer: race})
	})
	name := fmt.Sprintf("go-race-%d", time.Now().UnixNano())
	rec := l.Call("POST", "/api/v2/users", `{"username":"`+name+`","password":"Go-test-1!","name":"Go race","email":"`+name+`@example.test"}`, "Idempotency-Key", name)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	id, _ := created["id"].(string)
	t.Cleanup(func() { l.Call("DELETE", "/api/v2/users/"+id, "") })
	etag := rec.Header().Get("ETag")
	if id == "" || etag == "" {
		t.Fatalf("create: %v", created)
	}

	var wg sync.WaitGroup
	got := make([]*httptest.ResponseRecorder, 2)
	for i, given := range []string{"Alpha", "Beta"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = l.Call("PATCH", "/api/v2/users/"+id+"/profile", `{"given_name":"`+given+`"}`, "If-Match", etag)
		}()
	}
	wg.Wait()
	ok, refused := 0, 0
	var winner string
	for i, rec := range got {
		switch rec.Code {
		case 200:
			ok++
			var row map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &row)
			winner, _ = row["given_name"].(string)
		case 412:
			refused++
		default:
			t.Errorf("request %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("want one 200 and one 412, got %d and %d (%d %d)", ok, refused, got[0].Code, got[1].Code)
	}
	var row map[string]any
	_ = json.Unmarshal(l.Row(t, "get_user", pgtx.Args{"id": id}), &row)
	if row["given_name"] != winner {
		t.Fatalf("the row holds %v, the request that was answered 200 wrote %q", row["given_name"], winner)
	}
}
