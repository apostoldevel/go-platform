//go:build integration

package job

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/pgtx/pgtxtest"
	"github.com/jackc/pgx/v5"
)

// The environment is go-platform's own, as the configuration project's itest
// harness uses it: GO_TEST_PG_DSN (the pool, as the role under test — apibot,
// or daemon where the database takes the daemon road), GO_TEST_ADMIN_DSN
// (mints the session only), GO_TEST_AUDIENCE (the client id the token is
// issued for — on the daemon road daemon.begin verifies the bearer again, so
// a token signed by the test's own key is not enough; without it the direct
// road is taken). Without them every test here skips.

const agent = "go-jobs-test"

// tokens are the database's tokens of the sessions this run minted.
var tokens = &pgtxtest.Tokens{}

// start opens the pool on GO_TEST_PG_DSN and mounts the package under the
// platform host, the runner wrapped so the database gets the token it issued.
func start(t *testing.T) (*pgtx.Runner, http.Handler) {
	t.Helper()
	dsn := os.Getenv("GO_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("GO_TEST_PG_DSN not set")
	}
	pool, err := pgtx.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	runner := &pgtx.Runner{Pool: pool}
	if err := runner.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Logf("database features: %+v", runner.Features)
	h, err := platform.New(platform.Config{Keys: keys, Catalogue: runner}, New(Config{Doer: tokens.Doer(runner)}))
	if err != nil {
		t.Fatal(err)
	}
	return runner, h
}

// admin is the administrator's session minted on GO_TEST_ADMIN_DSN, signed
// out at the end.
func admin(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("GO_TEST_ADMIN_DSN")
	if dsn == "" {
		t.Skip("GO_TEST_ADMIN_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	var code, tok string
	if audience := os.Getenv("GO_TEST_AUDIENCE"); audience != "" {
		code, tok, err = pgtxtest.Login(context.Background(), conn, audience, cfg.User, cfg.Password, agent)
	} else {
		err = conn.QueryRow(context.Background(), "SELECT session FROM api.login($1, $2, $3, '127.0.0.1')", cfg.User, cfg.Password, agent).Scan(&code)
	}
	conn.Close(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tokens.Add(code, tok)
	t.Cleanup(func() {
		if c, err := pgx.Connect(context.Background(), dsn); err == nil {
			_, _ = c.Exec(context.Background(), "SELECT api.signout($1)", code)
			c.Close(context.Background())
		}
	})
	return code
}

// call runs one request through the host under the session.
func call(h http.Handler, code, method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token(code))
	req.Header.Set("X-Request-Id", "it-"+method)
	req.Header.Set("User-Agent", agent)
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// direct calls one api.* function by name under the session, through the
// road the package takes. refPath is a route any session may run under —
// the level of the function called is the allow list's, not the route's.
func direct(t *testing.T, runner *pgtx.Runner, code, fn string, args pgtx.Args) []map[string]any {
	t.Helper()
	var out []map[string]any
	err := runner.Do(context.Background(), pgtx.Session{Code: code, Token: tokens.Token(code), Agent: agent, Host: "127.0.0.1"}, &pgtx.Request{Method: "GET", Path: "/api/v2/me"}, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := pgtx.Call(ctx, tx, fn, args)
		if err != nil {
			return err
		}
		for _, row := range rows {
			var m map[string]any
			if len(row) > 0 {
				if err := json.Unmarshal(row, &m); err != nil {
					return fmt.Errorf("%s: a row that is not an object: %s", fn, row)
				}
			}
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("%s: %v", fn, err)
	}
	return out
}

// byCode is the id of the row of api.list_<x> whose code is code — the
// schedulers and programs the platform's init seeds.
func byCode(t *testing.T, runner *pgtx.Runner, session, list, code string) string {
	t.Helper()
	filter, _ := json.Marshal(map[string]string{"code": code})
	rows := direct(t, runner, session, list, pgtx.Args{"filter": json.RawMessage(filter), "limit": 0})
	if len(rows) != 1 {
		t.Fatalf("%s %s: %d rows", list, code, len(rows))
	}
	return rows[0]["id"].(string)
}

// pair is two settled rows of api.list_job in code order — two objects every
// stand has: the platform's init seeds the standard jobs.
func pair(t *testing.T, runner *pgtx.Runner, session string) [2]string {
	t.Helper()
	cut := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	search, _ := json.Marshal([]map[string]any{{"field": "created", "compare": "LSS", "value": cut}})
	rows := direct(t, runner, session, "list_job", pgtx.Args{"search": json.RawMessage(search), "limit": 2, "offset": 0, "orderby": json.RawMessage(`["code ASC"]`)})
	if len(rows) < 2 {
		t.Fatalf("fewer than two settled rows of job: %d — seed two at least ten minutes before the run", len(rows))
	}
	return [2]string{rows[0]["id"].(string), rows[1]["id"].(string)}
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return m
}

func TestIntegration_JobLifecycle(t *testing.T) {
	runner, h := start(t)
	code := admin(t)
	tag := fmt.Sprintf("%06d", time.Now().UnixNano()%1_000_000)

	// list
	rec := call(h, code, "GET", "/api/v2/jobs?page[limit]=2&sort=-created&fields=id,code", "")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	// q= over code/label
	if rec := call(h, code, "GET", "/api/v2/jobs?q=CHECK&fields=id", ""); rec.Code != 200 {
		t.Fatalf("q: %d %s", rec.Code, rec.Body)
	}

	scheduler := byCode(t, runner, code, "list_scheduler", "EACH_01_MINUTES")
	program := byCode(t, runner, code, "list_program", "CHECK_OFFLINE")

	// create with Idempotency-Key, replayed
	body := `{"code":"GO-` + tag + `","scheduler":"` + scheduler + `","program":"` + program + `","label":"Go test ` + tag + `"}`
	rec = call(h, code, "POST", Prefix, body, "Idempotency-Key", "go-job-"+tag)
	if rec.Code != 201 {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	created := decode(t, rec.Body.Bytes())
	id, _ := created["id"].(string)
	if id == "" || rec.Header().Get("Location") != Prefix+"/"+id || rec.Header().Get("ETag") == "" {
		t.Fatalf("create headers: %v id=%s", rec.Header(), id)
	}
	t.Cleanup(func() {
		for _, a := range []string{"delete", "drop"} {
			if rec := call(h, code, "POST", Prefix+"/"+id+"/actions/"+a, ""); rec.Code >= 300 && rec.Code != 404 {
				t.Logf("cleanup %s %s: %d %s", a, id, rec.Code, rec.Body.String())
			}
		}
	})
	if again := call(h, code, "POST", Prefix, body, "Idempotency-Key", "go-job-"+tag); again.Code != 201 || again.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay: %d %v", again.Code, again.Header())
	}

	// a taken code: api.add_job refuses with the catalogue's JobExists (a
	// 4xx), never edits the other job (api.set_job(NULL, …) would)
	if dup := call(h, code, "POST", Prefix, `{"code":"GO-`+tag+`","scheduler":"`+scheduler+`","program":"`+program+`","label":"Other"}`); dup.Code < 400 || dup.Code >= 500 {
		t.Fatalf("duplicate code: %d %s", dup.Code, dup.Body)
	}
	if again := call(h, code, "GET", Prefix+"/"+id, ""); strings.Contains(again.Body.String(), `"label":"Other"`) {
		t.Fatalf("the duplicate POST changed the job: %s", again.Body)
	}

	// get + ETag + 304
	rec = call(h, code, "GET", Prefix+"/"+id, "")
	if rec.Code != 200 || rec.Header().Get("ETag") == "" {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	etag := rec.Header().Get("ETag")
	if nm := call(h, code, "GET", Prefix+"/"+id, "", "If-None-Match", etag); nm.Code != 304 {
		t.Fatalf("304: %d", nm.Code)
	}

	// patch with stale / right ETag
	if st := call(h, code, "PATCH", Prefix+"/"+id, `{"label":"patched"}`, "If-Match", `W/"stale"`); st.Code != 412 {
		t.Fatalf("412: %d %s", st.Code, st.Body)
	}
	rec = call(h, code, "PATCH", Prefix+"/"+id, `{"label":"patched `+tag+`"}`, "If-Match", etag)
	if rec.Code != 200 {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}

	// methods
	rec = call(h, code, "GET", Prefix+"/"+id+"/methods", "")
	if rec.Code != 200 {
		t.Fatalf("methods: %d %s", rec.Code, rec.Body)
	}
}

// TestIntegration_JobWriteEcho is the third layer of the write check (T237),
// hand-written beside the package (the configuration project's itest harness
// cannot be imported across the module boundary): every writable key of the
// body is written with a value of its own — on POST and again, changed, on
// PATCH — and read back through the package and through api.get_job.
//
// daterun is excused on POST: db.ft_job_insert recomputes dateRun from the
// scheduler's period whenever a scheduler is set — and db.job.scheduler is
// NOT NULL — so a sent daterun cannot read back on create. On PATCH it is
// written with a value of its own. A timestamptz reads back in the session's
// zone, so daterun is compared as an instant, not as text.
func TestIntegration_JobWriteEcho(t *testing.T) {
	runner, h := start(t)
	code := admin(t)
	tag := fmt.Sprintf("%06d", time.Now().UnixNano()%1_000_000)
	parent := pair(t, runner, code)

	create := map[string]any{
		"parent":      parent[0],
		"type":        byCode(t, runner, code, "list_type", "disposable.job"),
		"scheduler":   byCode(t, runner, code, "list_scheduler", "EACH_01_MINUTES"),
		"program":     byCode(t, runner, code, "list_program", "CHECK_OFFLINE"),
		"code":        "GO-E-" + tag,
		"label":       "Go echo A " + tag,
		"description": "write on create",
	}
	patch := map[string]any{
		"parent":      parent[1],
		"type":        byCode(t, runner, code, "list_type", "periodic.job"),
		"scheduler":   byCode(t, runner, code, "list_scheduler", "EACH_05_MINUTES"),
		"program":     byCode(t, runner, code, "list_program", "CHECK_SESSION"),
		"daterun":     "2030-01-02T03:04:05+00:00",
		"code":        "GO-E2-" + tag,
		"label":       "Go echo B " + tag,
		"description": "write on patch",
	}

	sent := func(m map[string]any, keys ...string) map[string]any {
		out := map[string]any{}
		for _, k := range keys {
			out[k] = m[k]
		}
		return out
	}
	check := func(where string, row map[string]any, want map[string]any) {
		t.Helper()
		var bad []string
		for k, w := range want {
			g := row[k]
			switch k {
			case "daterun":
				gt, gerr := parseInstant(fmt.Sprint(g))
				wt, werr := parseInstant(fmt.Sprint(w))
				if gerr != nil || werr != nil || !gt.Equal(wt) {
					bad = append(bad, fmt.Sprintf("%s = %v, written %v", k, g, w))
				}
			default:
				if g == nil || fmt.Sprint(g) != fmt.Sprint(w) {
					bad = append(bad, fmt.Sprintf("%s = %v, written %v", k, g, w))
				}
			}
		}
		if len(bad) > 0 {
			t.Fatalf("%s reads back wrong:\n  %s", where, strings.Join(bad, "\n  "))
		}
	}

	raw, _ := json.Marshal(create)
	rec := call(h, code, "POST", Prefix, string(raw))
	if rec.Code != 201 {
		t.Fatalf("POST with the whole body: %d %s", rec.Code, rec.Body)
	}
	created := decode(t, rec.Body.Bytes())
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("POST answered without an id: %v", created)
	}
	t.Cleanup(func() {
		for _, a := range []string{"delete", "drop"} {
			if rec := call(h, code, "POST", Prefix+"/"+id+"/actions/"+a, ""); rec.Code >= 300 && rec.Code != 404 {
				t.Logf("cleanup %s %s: %d %s", a, id, rec.Code, rec.Body.String())
			}
		}
	})
	check("POST's answer", created, sent(create, "parent", "type", "scheduler", "program", "code", "label", "description"))
	if rec := call(h, code, "GET", Prefix+"/"+id, ""); rec.Code != 200 {
		t.Fatalf("GET after POST: %d %s", rec.Code, rec.Body)
	} else {
		check("GET after POST", decode(t, rec.Body.Bytes()), sent(create, "parent", "type", "scheduler", "program", "code", "label", "description"))
	}
	if rows := direct(t, runner, code, "get_job", pgtx.Args{"id": id}); len(rows) != 1 {
		t.Fatalf("api.get_job: %d rows", len(rows))
	} else {
		check("api.get_job after POST", rows[0], sent(create, "parent", "type", "scheduler", "program", "code", "label", "description"))
	}

	raw, _ = json.Marshal(patch)
	rec = call(h, code, "PATCH", Prefix+"/"+id, string(raw), "If-Match", call(h, code, "GET", Prefix+"/"+id, "").Header().Get("ETag"))
	if rec.Code != 200 {
		t.Fatalf("PATCH with the whole body: %d %s", rec.Code, rec.Body)
	}
	if rec := call(h, code, "GET", Prefix+"/"+id, ""); rec.Code != 200 {
		t.Fatalf("GET after PATCH: %d %s", rec.Code, rec.Body)
	} else {
		row := decode(t, rec.Body.Bytes())
		check("GET after PATCH", row, sent(patch, "parent", "type", "scheduler", "program", "daterun", "code", "label", "description"))
		// what POST wrote and PATCH did not touch reads back as POST wrote
		// it — a PATCH that nulls a column it was not given is red here
	}
	if rows := direct(t, runner, code, "get_job", pgtx.Args{"id": id}); len(rows) != 1 {
		t.Fatalf("api.get_job after PATCH: %d rows", len(rows))
	} else {
		check("api.get_job after PATCH", rows[0], sent(patch, "parent", "type", "scheduler", "program", "daterun", "code", "label", "description"))
	}
}

// parseInstant is the instant of a timestamp as it may come back from the
// database: RFC 3339, or PostgreSQL's own rendering ('…+03', '… 03:04:05'
// with an offset) — never parsed without its zone.
func parseInstant(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-07", "2006-01-02 15:04:05-07", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("not a timestamp: %s", s)
}

func TestIntegration_ForeignOrMissingIs404(t *testing.T) {
	_, h := start(t)
	code := admin(t)
	rec := call(h, code, "GET", Prefix+"/00000000-0000-4000-b000-00000000dead", "")
	if rec.Code != 404 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
