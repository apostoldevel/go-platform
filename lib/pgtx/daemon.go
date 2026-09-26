package pgtx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// daemonTx is the request transaction of the daemon road: Call on it goes
// through daemon.call, the only door the daemon role has to schema api.
type daemonTx struct{ pgx.Tx }

// roadKey marks the context of a handler on the daemon road: a transaction
// the handler derives (tx.Begin, a nested savepoint) is no daemonTx, and
// Call must not fall back to the direct call there — the allow list and the
// per-call context restore would be bypassed by a role that has both roads.
type roadKey struct{}

func onDaemonRoad(ctx context.Context, tx pgx.Tx) bool {
	if _, ok := tx.(daemonTx); ok {
		return true
	}
	return ctx.Value(roadKey{}) != nil
}

// doDaemon is Do on the daemon road. The database verifies the token, opens
// the session context and runs the guard of the route in daemon.begin, and
// completes the journal line in daemon.end; the handler's calls reach only
// the functions its allow list registers.
func (r *Runner) doDaemon(ctx context.Context, s Session, req *Request, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if req == nil {
		// begin guards the route by the path and journals every request: a
		// request without one is a caller's defect, not a client's
		return r.internal("begin", errors.New("pgtx: the daemon road needs the request"))
	}
	req.LogID = 0
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return r.internal("begin", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var host any
	if ip := net.ParseIP(s.Host); ip != nil {
		host = ip.String()
	}
	method := strings.ToUpper(req.Method)
	if method == http.MethodHead {
		method = http.MethodGet // the guard of a route knows GET, never HEAD
	}
	var payload any
	if len(req.Payload) > 0 {
		payload = req.Payload
	}
	var rid any
	if isUUID(req.RequestID) {
		rid = req.RequestID
	}
	var authorized bool
	var status *int32
	var code, message *string
	if err := tx.QueryRow(ctx, "SELECT authorized, status, error, message FROM daemon.begin($1, $2, $3::inet, $4, $5, $6::jsonb, $7::uuid)",
		s.Token, nilIfEmpty(s.Agent), host, method, req.Path, payload, rid).Scan(&authorized, &status, &code, &message); err != nil {
		// begin does not raise on a refusal: this is the connection or a
		// database that is not what Detect saw
		return r.internal("begin", err)
	}
	if !authorized {
		// the refusal's journal line is begin's; the commit keeps it — first,
		// under a context the client's hang-up does not cancel, and before
		// the problem is titled (a catalogue lookup takes a pool connection)
		jctx, cancel := journalCtx(ctx)
		defer cancel()
		if err := tx.Commit(jctx); err != nil && r.Logger != nil {
			r.Logger.Error("refusal not journalled", "where", "commit", "path", req.Path, "request_id", req.RequestID, "err", err)
		}
		return r.refusal(ctx, req, status, code, message)
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT request"); err != nil {
		return r.internal("savepoint", err)
	}
	if err := fn(context.WithValue(ctx, roadKey{}, true), daemonTx{tx}); err != nil {
		p := r.explain(ctx, req, err)
		// undo the handler's work; the request row was written before the
		// savepoint and end completes the journal line after it
		jctx, cancel := journalCtx(ctx)
		defer cancel()
		if _, rerr := tx.Exec(jctx, "ROLLBACK TO SAVEPOINT request"); rerr != nil {
			if r.Logger != nil {
				r.Logger.Error("refusal not journalled", "where", "rollback to savepoint", "err", rerr)
			}
			return p
		}
		status, text := refusalOf(p, err)
		if jerr := r.end(jctx, tx, req, status, text); jerr != nil {
			req.LogID = 0
			if r.Logger != nil {
				r.Logger.Error("refusal not completed in the journal", "where", "daemon.end", "status", status, "path", req.Path, "request_id", req.RequestID, "err", jerr)
			}
			r.salvage(jctx, tx, req)
			return p
		}
		if jerr := tx.Commit(jctx); jerr != nil {
			req.LogID = 0
			if r.Logger != nil {
				r.Logger.Error("refusal not journalled", "where", "commit", "status", status, "path", req.Path, "request_id", req.RequestID, "err", jerr)
			}
		}
		return p
	}
	if err := r.end(ctx, tx, req, req.status(), nil); err != nil {
		// the handler succeeded and the request cannot be closed: a defect of
		// the road (no open request, an aborted transaction), never the
		// client's — 500, the handler's work undone, begin's line kept
		req.LogID = 0
		p := r.internal("end", err)
		jctx, cancel := journalCtx(ctx)
		defer cancel()
		r.salvage(jctx, tx, req)
		return p
	}
	if err := tx.Commit(ctx); err != nil {
		req.LogID = 0 // the deferred rollback discards the row
		return r.explain(ctx, req, err)
	}
	return nil
}

// salvage keeps begin's journal line when end could not complete it: the
// handler's work is undone to the savepoint and the transaction commits with
// the line alone — status unknown, but the request is on record.
func (r *Runner) salvage(ctx context.Context, tx pgx.Tx, req *Request) {
	_, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT request")
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil && r.Logger != nil {
		r.Logger.Error("request not journalled", "path", req.Path, "request_id", req.RequestID, "err", err)
	}
}

// end completes the journal line of the request: the status on the wire and,
// on a refusal, its text — daemon.end takes the catalogue code from it and
// never rewrites the status given.
func (r *Runner) end(ctx context.Context, tx pgx.Tx, req *Request, status int, text any) error {
	var id *int64
	if err := tx.QueryRow(ctx, "SELECT log_id FROM daemon.end($1, $2)", status, text).Scan(&id); err != nil {
		return err
	}
	if id != nil {
		req.LogID = *id
	}
	return nil
}

// refusalOf is what daemon.end journals for a refused request: the problem's
// status, and the text its catalogue code can be read from — the database's
// own message where the refusal came from it, "code: detail" for a module's
// problem that carries a code, the detail otherwise.
func refusalOf(p, cause error) (int, any) {
	var pg *pgconn.PgError
	if errors.As(cause, &pg) {
		var pr *problem.Problem
		status := 500
		if errors.As(p, &pr) {
			status = pr.Status
		}
		return status, pg.Message
	}
	var pr *problem.Problem
	if !errors.As(p, &pr) {
		return 500, nil
	}
	if pr.Code != nil {
		return pr.Status, *pr.Code + ": " + pr.Detail
	}
	if pr.Detail != "" {
		return pr.Status, pr.Detail
	}
	return pr.Status, nil
}

// codeNotAccepted is daemon.begin's 401 for any failure of the identity step
// that is not itself a 401 — a token of an unknown issuer, and also a
// statement timeout or a deadlock: its text is logged, not sent.
const codeNotAccepted = "ERR-401-002"

// refusal is the problem for what daemon.begin refused: a token the database
// does not accept is a 401 with its code (the host's challenge goes with
// it), a route the guard closes a 403 (ERR-403-010), anything else by its
// catalogue code.
func (r *Runner) refusal(ctx context.Context, req *Request, status *int32, code, message *string) error {
	st, c, detail := http.StatusInternalServerError, "", ""
	if status != nil {
		st = int(*status)
	}
	if code != nil {
		c = *code
	}
	if message != nil {
		detail = *message
	}
	if st == http.StatusUnauthorized {
		if c == "" {
			c = problem.CodeLoginFailed
		}
		if c == codeNotAccepted {
			// the wrapped text may be the database's own failure, not the
			// client's: it goes to the log, the client gets the verdict
			if r.Logger != nil {
				r.Logger.Warn("token not accepted by the database", "path", req.Path, "request_id", req.RequestID, "reason", detail)
			}
			detail = "The access token was not accepted."
		}
		return problem.Unauthorized(r, c, detail)
	}
	if c != "" {
		p := problem.FromCode(c, r.catalogueTitle(ctx, c, detail), detail)
		p.Status = st // the database said which status; the code's group is a guess
		return p
	}
	return r.internal("begin", fmt.Errorf("refused without a code: %d %s", st, detail))
}

// catalogueTitle is the catalogue message of code for a problem's title, read
// without a session (daemon.error, or api.get_error_by_code before 1.2.31);
// fallback when the catalogue has none, or only a format template.
func (r *Runner) catalogueTitle(ctx context.Context, code, fallback string) string {
	if t := r.titles[code]; t != "" {
		return t
	}
	if r.Pool == nil {
		return fallback
	}
	// the catalogue does not change under a running process: one lookup per
	// code, an unusable answer remembered as "" (the fallback)
	if t, ok := r.looked.Load(code); ok {
		if t.(string) != "" {
			return t.(string)
		}
		return fallback
	}
	lookup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	sql := "SELECT message FROM api.get_error_by_code($1)"
	if r.Features.Daemon {
		sql = "SELECT e->>'message' FROM daemon.error($1) e"
	}
	var msg *string
	err := r.Pool.QueryRow(lookup, sql, code).Scan(&msg)
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		t := ""
		if msg != nil && usableTitle(*msg) {
			t = *msg
		}
		r.looked.Store(code, t)
		if t != "" {
			return t
		}
	}
	return fallback
}

// Route is one route of an API version the database guards: a path node
// (/api/v2/users) and a method its guard answers for.
type Route struct {
	Path   string
	Method string
}

// Routes are the database's routes of an API version (daemon.routes): what a
// process checks its prefixes against before it announces them. Nil, no
// error, off the daemon road — there the database guards no route.
func (r *Runner) Routes(ctx context.Context, version string) ([]Route, error) {
	if !r.Features.Daemon {
		return nil, nil
	}
	rows, err := r.Pool.Query(ctx, "SELECT path, method FROM daemon.routes($1)", version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Route{} // the daemon road with no route is "everything closed", never nil
	for rows.Next() {
		var rt Route
		if err := rows.Scan(&rt.Path, &rt.Method); err != nil {
			return nil, err
		}
		out = append(out, rt)
	}
	return out, rows.Err()
}

// callDaemon is Call on the daemon road: the name and the argument object go
// to daemon.call, which chooses the registered form by the keys and builds
// the call from the catalogue — no text of SQL over schema api leaves Go.
func callDaemon(ctx context.Context, tx pgx.Tx, fn string, args Args) ([]json.RawMessage, error) {
	name := strings.TrimPrefix(fn, "api.")
	if !callIdent.MatchString(name) {
		return nil, fmt.Errorf("pgtx: %q is not a function of schema api", fn)
	}
	for k, v := range args {
		if !callIdent.MatchString(k) {
			return nil, fmt.Errorf("pgtx: %s: %q is not a parameter name", fn, k)
		}
		if _, bare := v.([]byte); bare {
			return nil, fmt.Errorf("pgtx: %s: %q is []byte — pass JSON as json.RawMessage", fn, k)
		}
	}
	body, err := args.JSON()
	if err != nil {
		return nil, fmt.Errorf("pgtx: %s: %w", fn, err)
	}
	rows, err := tx.Query(ctx, "SELECT c FROM daemon.call($1, $2::jsonb) c", name, string(body))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var row []byte
		if err := rows.Scan(&row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
