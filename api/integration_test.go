//go:build integration

package api

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"strconv"
	"testing"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/internal/resttest"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

func live(t *testing.T) *resttest.Live {
	return resttest.Start(t, "go-api-test", func(r *pgtx.Runner) platform.Module { return New(Config{Doer: r}) })
}

func TestIntegration_APILogReadParity(t *testing.T) {
	l := live(t)
	rec := l.Call("GET", "/api/v2/api-log?sort=-datetime&page[limit]=5", "")
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != 200 || list.Total < 1 || len(list.Items) == 0 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	rec = l.Call("GET", "/api/v2/api-log/"+strconv.FormatInt(list.Items[0].ID, 10), "")
	var want json.RawMessage
	if err := l.Runner.Do(context.Background(), l.Session, &pgtx.Request{Method: "TEST", Path: "/parity"}, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT row_to_json(t) FROM api.get_log($1::bigint) t", list.Items[0].ID).Scan(&want)
	}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || !resttest.SameJSON(rec.Body.Bytes(), want) {
		t.Fatalf("get/parity: %d\n v2 %s\n v1 %s", rec.Code, rec.Body, want)
	}
	if rec = l.Call("GET", "/api/v2/api-log/999999999999", ""); rec.Code != 404 {
		t.Fatalf("missing row: %d %s", rec.Code, rec.Body)
	}
}
