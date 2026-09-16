//go:build integration

package log

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"strconv"
	"strings"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-log-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

func TestIntegration_EventLogReadParity(t *testing.T) {
	l := live(t)

	// list, newest first; one row for parity with api.get_event_log
	rec := l.Call("GET", "/api/v2/event-log?sort=-datetime&page[limit]=5", "")
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || list.Total < 1 || len(list.Items) == 0 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	id := strconv.FormatInt(list.Items[0].ID, 10)
	rec = l.Call("GET", "/api/v2/event-log/"+id, "")
	var want json.RawMessage
	if err := l.Runner.Do(context.Background(), l.Session, &pgtx.Request{Method: "TEST", Path: "/parity"}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT row_to_json(t) FROM api.get_event_log($1::bigint) t", list.Items[0].ID).Scan(&want)
	}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), want) {
		t.Fatalf("get/parity: %d\n v2 %s\n v1 %s", rec.Code, rec.Body, want)
	}
	if rec = l.Call("GET", "/api/v2/event-log/999999999999", ""); rec.Code != 404 {
		t.Fatalf("missing row: %d %s", rec.Code, rec.Body)
	}
	// the user's journal: one row is fast; the list is not exercised here —
	// api.count_user_log(NULL) takes ~42 s on the dev database (api.user_log
	// joins EventLog by username through a CTE — a database change), and v1's
	// /event/log/count pays the same
	if rec = l.Call("GET", "/api/v2/me/event-log/"+id, ""); rec.Code != 200 && rec.Code != 404 {
		t.Fatalf("me/event-log row: %d %s", rec.Code, rec.Body)
	}
}

// A journal write through api.write_to_log (fixed in db-platform 1.2.21:
// before, the function's own call was ambiguous) — the row is read back by
// its Location.
func TestIntegration_EventLogWrite(t *testing.T) {
	l := live(t)
	rec := l.Call("POST", "/api/v2/event-log", `{"type":"M","code":1000,"scope":"go-test","text":"written by the v2 integration test"}`)
	if rec.Code != 201 || rec.Header().Get("Location") == "" {
		t.Fatalf("write: %d %s", rec.Code, rec.Body)
	}
	if rec = l.Call("GET", rec.Header().Get("Location"), ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "written by the v2") {
		t.Fatalf("written row: %d %s", rec.Code, rec.Body)
	}
}
