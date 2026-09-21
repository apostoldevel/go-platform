// Package pgtx runs one HTTP request as one database transaction:
//
//	BEGIN
//	  SELECT * FROM api.authorize_local($sub, $agent, $host)
//	  SAVEPOINT request                    -- when the database journals (gateway patch)
//	  … the handler's api.* calls …
//	  SELECT api.log_request(…, status)    -- 2xx: in the same transaction
//	COMMIT
//
// A refusal is journalled too: the handler's work is undone with ROLLBACK
// TO SAVEPOINT, the error is explained by the catalogue in a separate
// transaction (the aborted one cannot run the query that explains its own
// error), then api.log_request(…, 4xx) runs under the session context that
// api.authorize_local set before the savepoint — it survives the partial
// rollback — and the transaction commits with the audit row alone. A
// session the database refuses (401: unknown, or raised — locked user,
// expired password, IP table) is journalled without a session, in a fresh
// transaction when authorize aborted the request's one.
//
// Session context is per transaction here — under a pool the next request
// would otherwise inherit a stranger's session — so every api.* call of a
// request goes through the same tx, and the pool runs RESET ALL on release
// as the second-layer guard.
package pgtx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Session identifies the caller: the session code from the verified JWT's
// `sub`, the client's User-Agent and address (the host decides it from
// X-Forwarded-For and the peer; platform.Config.TrustedProxies).
type Session struct {
	Code  string
	Agent string
	Host  string
}

// Features are the api.* functions of the gateway patch the database
// has; Detect fills them, so one binary runs before and after the patch.
type Features struct {
	AuthorizeLocal bool // api.authorize_local — session context set transaction-locally
	LogRequest     bool // api.log_request(method, path, payload, status, runtime, request_id)
	ParseMessage   bool // api.parse_message — the catalogue cut done by the database
}

// Runner holds the pool and what the database offers.
type Runner struct {
	Pool     *pgxpool.Pool
	Features Features
	Logger   *slog.Logger
}

// Request describes the HTTP request for api.log_request. The handler may
// set Status inside fn (201, 204); default 200. On a refusal the status
// journalled is the problem's. LogID is the db.api_log row Do wrote, 0 when
// the database does not journal or Do could not confirm the row.
type Request struct {
	Method    string
	Path      string
	Payload   []byte // jsonb or nil
	RequestID string // X-Request-Id, a UUID
	Status    int
	LogID     int64
}

func (r *Request) status() int {
	if r == nil || r.Status == 0 {
		return 200
	}
	return r.Status
}

// Detect asks pg_proc which gateway-patch functions exist.
func (r *Runner) Detect(ctx context.Context) error {
	rows, err := r.Pool.Query(ctx, "SELECT proname FROM pg_proc WHERE pronamespace = 'api'::regnamespace AND proname IN ('authorize_local', 'log_request', 'parse_message')")
	if err != nil {
		return err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return err
		}
		names = append(names, n)
	}
	r.Features = featuresFrom(names)
	return rows.Err()
}

func featuresFrom(names []string) Features {
	var f Features
	for _, n := range names {
		switch n {
		case "authorize_local":
			f.AuthorizeLocal = true
		case "log_request":
			f.LogRequest = true
		case "parse_message":
			f.ParseMessage = true
		}
	}
	return f
}

func (r *Runner) authorizeSQL() string {
	if r.Features.AuthorizeLocal {
		return "SELECT authorized, message FROM api.authorize_local($1, $2, $3::inet)"
	}
	return "SELECT authorized, message FROM api.authorize($1, $2, $3::inet)"
}

// NewPool opens a pgx pool that resets session state on every release.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.AfterRelease = func(conn *pgx.Conn) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := conn.Exec(ctx, "RESET ALL")
		return err == nil // a connection that cannot be reset is dropped
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

// Do runs fn inside the request transaction. The error returned is always a
// *problem.Problem when the request failed for the client's reasons, so the
// handler can write it as is.
func (r *Runner) Do(ctx context.Context, s Session, req *Request, fn func(ctx context.Context, tx pgx.Tx) error) error {
	started := time.Now()
	if req != nil {
		req.LogID = 0
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return r.internal("begin", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var authorized bool
	var message *string
	var host any
	if ip := net.ParseIP(s.Host); ip != nil {
		host = ip.String()
	}
	journal := r.Features.LogRequest && req != nil
	if err := tx.QueryRow(ctx, r.authorizeSQL(), s.Code, nilIfEmpty(s.Agent), host).Scan(&authorized, &message); err != nil {
		p := r.explain(ctx, err)
		if journal {
			// the refusal aborted the transaction before any savepoint; there
			// is no session context to keep, so a fresh one journals it
			jctx, cancel := journalCtx(ctx)
			defer cancel()
			_ = tx.Rollback(jctx)
			if fresh, berr := r.Pool.Begin(jctx); berr == nil {
				r.journalRefusal(jctx, fresh, req, started, p)
				_ = fresh.Rollback(jctx)
			} else if r.Logger != nil {
				r.Logger.Error("refusal not journalled", "where", "begin", "err", berr)
			}
		}
		return p
	}
	if !authorized {
		detail := ""
		if message != nil {
			detail = *message
		}
		p := problem.New(401, "unauthorized", "Unauthorized", detail)
		if journal {
			// no session context to lose: the row carries the path and the
			// status, api_log.session stays empty
			jctx, cancel := journalCtx(ctx)
			defer cancel()
			r.journalRefusal(jctx, tx, req, started, p)
		}
		return p
	}
	if journal {
		if _, err := tx.Exec(ctx, "SAVEPOINT request"); err != nil {
			return r.internal("savepoint", err)
		}
	}
	if err := fn(ctx, tx); err != nil {
		p := r.explain(ctx, err)
		if journal {
			// undo the handler's work, keep the session context set before
			// the savepoint; a transaction that cannot roll back to it
			// (connection gone) has nothing to journal on
			jctx, cancel := journalCtx(ctx)
			defer cancel()
			if _, rerr := tx.Exec(jctx, "ROLLBACK TO SAVEPOINT request"); rerr == nil {
				r.journalRefusal(jctx, tx, req, started, p)
			} else if r.Logger != nil {
				r.Logger.Error("refusal not journalled", "where", "rollback to savepoint", "err", rerr)
			}
		}
		return p
	}
	if journal {
		if err := r.logRequest(ctx, tx, req, started, req.status()); err != nil {
			return r.explain(ctx, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		if req != nil {
			req.LogID = 0 // the deferred rollback discards the row
		}
		return r.explain(ctx, err)
	}
	return nil
}

// journalCtx is the context the audit row is written under: the client
// that gave up on the answer does not take the row with it.
func journalCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
}

// logRequest writes the db.api_log row for the request's outcome.
func (r *Runner) logRequest(ctx context.Context, tx pgx.Tx, req *Request, started time.Time, status int) error {
	var payload any
	if len(req.Payload) > 0 {
		payload = req.Payload
	}
	var rid any
	if req.RequestID != "" && isUUID(req.RequestID) {
		rid = req.RequestID
	}
	return tx.QueryRow(ctx, "SELECT api.log_request($1, $2, $3::jsonb, $4, $5::interval, $6::uuid)", req.Method, req.Path, payload, status, time.Since(started), rid).Scan(&req.LogID)
}

// journalRefusal commits the audit row of a refused request under a
// journalCtx; the answer to the client is the problem either way — an audit
// that cannot be written is logged, not turned into a second error.
func (r *Runner) journalRefusal(ctx context.Context, tx pgx.Tx, req *Request, started time.Time, p error) {
	status := 500
	var pr *problem.Problem
	if errors.As(p, &pr) {
		status = pr.Status
	}
	err := r.logRequest(ctx, tx, req, started, status)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		req.LogID = 0
		if r.Logger != nil {
			r.Logger.Error("refusal not journalled", "where", "log_request", "status", status, "err", err)
		}
	}
}

type kind int

const (
	kindProblem    kind = iota // already a *problem.Problem
	kindCatalogue              // RAISE 'ERR-GGG-CCC: text'
	kindRaise                  // RAISE without a catalogue code
	kindConstraint             // SQLSTATE class 23: a constraint refused the client's data
	kindData                   // SQLSTATE class 22: the client's data did not read (a bad uuid, a number out of range, bad base64)
	kindInternal               // anything else
)

// classify sorts an error from inside the transaction.
func classify(err error) (code, detail string, k kind) {
	var p *problem.Problem
	if errors.As(err, &p) {
		return "", "", kindProblem
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "P0001" {
		if code, text, ok := problem.ParseMessage(pg.Message); ok {
			return code, text, kindCatalogue
		}
		return "", pg.Message, kindRaise
	}
	if pg != nil && strings.HasPrefix(pg.Code, "23") {
		return pg.Code, pg.Message, kindConstraint
	}
	if pg != nil && strings.HasPrefix(pg.Code, "22") {
		return pg.Code, pg.Message, kindData
	}
	return "", "", kindInternal
}

// explain turns the failure into a problem; the catalogue is consulted in a
// fresh transaction because the failed one cannot run queries any more.
func (r *Runner) explain(ctx context.Context, err error) error {
	code, detail, k := classify(err)
	switch k {
	case kindProblem:
		return err
	case kindCatalogue:
		title := detail
		if r.Pool != nil {
			lookup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			if r.Features.ParseMessage {
				// the database's own cut of the message, in a separate transaction
				var pgErr *pgconn.PgError
				errors.As(err, &pgErr)
				var c int
				var m, e string
				if qerr := r.Pool.QueryRow(lookup, "SELECT code, message, error FROM api.parse_message($1)", pgErr.Message).Scan(&c, &m, &e); qerr == nil && m != "" {
					detail = m
				}
			}
			var msg string
			// the catalogue message may be a format template ("… \"%s\" …"); a
			// template is no title — the raised text (already filled in) is
			if qerr := r.Pool.QueryRow(lookup, "SELECT message FROM api.get_error_by_code($1)", code).Scan(&msg); qerr == nil && msg != "" && !strings.Contains(msg, "%") {
				title = msg
			}
		}
		return problem.FromCode(code, title, detail)
	case kindRaise:
		return problem.New(400, "raise", "Request refused", detail)
	case kindConstraint:
		// the constraint's own text, as v1 sends it; pg Detail carries the
		// failing row or the duplicate key and stays out of the answer.
		// Logged: a platform defect surfacing as a constraint would otherwise
		// be indistinguishable from bad input
		if r.Logger != nil {
			r.Logger.Warn("constraint refused", "sqlstate", code, "err", detail)
		}
		if code == "23505" { // unique_violation
			return problem.New(409, "conflict", "Conflict", detail)
		}
		return problem.New(400, "validation", "Bad request", detail)
	case kindData:
		// what the database could not read as the type it expected is the
		// client's to fix, not an internal failure — the same 400 v1 sends
		return problem.New(400, "validation", "Bad request", detail)
	}
	return r.internal("request", err)
}

func (r *Runner) internal(where string, err error) error {
	if r.Logger != nil {
		r.Logger.Error("database failure", "where", where, "err", err)
	}
	return problem.New(500, "internal", "Internal error", fmt.Sprintf("%s failed", where))
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
