// Package pgtx runs one HTTP request as one database transaction
// (contract K7):
//
//	BEGIN
//	  SELECT * FROM api.authorize($sub, $agent, $host)
//	  … the handler's api.* calls …
//	  SELECT api.log_request(…)            -- when the database has it (gateway patch)
//	COMMIT | ROLLBACK → the error is explained by the catalogue in a
//	                    separate transaction → problem+json
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
// `sub`, the client's User-Agent and address (X-Forwarded-For).
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
// set Status inside fn (201, 204); default 200.
type Request struct {
	Method    string
	Path      string
	Payload   []byte // jsonb or nil
	RequestID string // X-Request-Id, a UUID
	Status    int
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
	if err := tx.QueryRow(ctx, r.authorizeSQL(), s.Code, nilIfEmpty(s.Agent), host).Scan(&authorized, &message); err != nil {
		return r.explain(ctx, err)
	}
	if !authorized {
		detail := ""
		if message != nil {
			detail = *message
		}
		return problem.New(401, "unauthorized", "Unauthorized", detail)
	}
	if err := fn(ctx, tx); err != nil {
		return r.explain(ctx, err)
	}
	if r.Features.LogRequest && req != nil {
		var payload any
		if len(req.Payload) > 0 {
			payload = req.Payload
		}
		var rid any
		if req.RequestID != "" && isUUID(req.RequestID) {
			rid = req.RequestID
		}
		if _, err := tx.Exec(ctx, "SELECT api.log_request($1, $2, $3::jsonb, $4, $5::interval, $6::uuid)", req.Method, req.Path, payload, req.status(), time.Since(started), rid); err != nil {
			return r.explain(ctx, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return r.explain(ctx, err)
	}
	return nil
}

type kind int

const (
	kindProblem    kind = iota // already a *problem.Problem
	kindCatalogue              // RAISE 'ERR-GGG-CCC: text'
	kindRaise                  // RAISE without a catalogue code
	kindConstraint             // SQLSTATE class 23: a constraint refused the client's data
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
				// the database's own cut of the message, separate transaction (§3)
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
