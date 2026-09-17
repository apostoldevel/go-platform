// Package rest is what every GoAPI package repeats over api.*: the
// conventions of /api/v2 — a list with total and paging, a
// single row with ETag and 304, problem+json for any error, JSON bodies
// with unknown keys refused (as CheckJsonbKeys does), Idempotency-Key replay.
// A package supplies its api.* names and its writable body; the shape of the
// wire is here. It mirrors the SQL schema `rest` in role: the dispatcher
// convention, not the business.
package rest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/query"
	"github.com/jackc/pgx/v5"
)

// Doer runs the request transaction (pgtx.Runner in production).
type Doer interface {
	Do(ctx context.Context, s pgtx.Session, req *pgtx.Request, fn func(context.Context, pgx.Tx) error) error
}

// Resource names the api.* functions of one resource. Get takes ($1::uuid),
// List takes (search, filter, limit, offset, orderby), Count takes (search,
// filter) — the shapes db-platform generates for every entity and for the
// admin module's users/groups/areas/interfaces alike.
type Resource struct {
	Prefix  string // "/api/v2/users"
	GetFn   string // "api.get_user"
	ListFn  string // "api.list_user"
	CountFn string // "api.count_user"
	IntID   bool   // the id is a bigint (journals), not a uuid
}

// ReqOf describes the request for api.log_request; status is what the handler
// will answer on success.
func ReqOf(r *http.Request, status int, payload []byte) *pgtx.Request {
	return &pgtx.Request{Method: r.Method, Path: r.URL.Path, Payload: payload, RequestID: r.Header.Get("X-Request-Id"), Status: status}
}

// Fail writes any error as problem+json; a non-problem becomes 500 and is logged.
func Fail(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var p *problem.Problem
	if !errors.As(err, &p) {
		if log == nil {
			log = slog.Default()
		}
		log.Error("unexpected error", "request_id", r.Header.Get("X-Request-Id"), "err", err)
		p = problem.New(500, "internal", "Internal error", "")
	}
	p.Write(w, r)
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUUID says whether s has the shape of a UUID.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }

// IDOf is the {id} path value, refused before the database when it is not a UUID.
func IDOf(r *http.Request) (string, error) {
	id := r.PathValue("id")
	if !IsUUID(id) {
		return "", problem.New(400, "validation", "Bad request", "id must be a UUID")
	}
	return id, nil
}

// IntIDOf is the {id} path value of a journal row: a positive integer.
func IntIDOf(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, problem.New(400, "validation", "Bad request", "id must be a positive integer")
	}
	return id, nil
}

// id is the {id} of this resource in the type its api.* takes.
func (res Resource) id(r *http.Request) (any, error) {
	if res.IntID {
		return IntIDOf(r)
	}
	return IDOf(r)
}

// WriteJSON writes a JSON body with the status.
func WriteJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// ETagOf derives the weak ETag of a row: from its last update where the view
// exposes one (`lastupdate` of Object<X>, or `udate`), otherwise a
// digest of the row itself — api.user and the admin views carry no stamp,
// and If-Match must still be able to say "unchanged since I read it".
func ETagOf(row json.RawMessage) string {
	if len(row) == 0 || !json.Valid(row) {
		return ""
	}
	var u struct {
		LastUpdate string `json:"lastupdate"`
		Udate      string `json:"udate"`
	}
	_ = json.Unmarshal(row, &u)
	if u.LastUpdate == "" {
		u.LastUpdate = u.Udate
	}
	if u.LastUpdate != "" {
		return `W/"` + u.LastUpdate + `"`
	}
	return `W/"` + hashOf(row)[:32] + `"`
}

func hashOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// ReadBody decodes a JSON object body, refusing unknown keys; an empty body
// is not an error — the handler decides whether it needs one.
func ReadBody(r *http.Request, into any) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, problem.New(400, "validation", "Bad request", "cannot read body")
	}
	if len(raw) == 0 {
		return raw, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return nil, problem.New(400, "validation", "Bad request", err.Error())
	}
	return raw, nil
}

// Project keeps only the requested fields (api.list_* has no pFields).
func Project(row json.RawMessage, fields []string) json.RawMessage {
	if len(fields) == 0 {
		return row
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(row, &m) != nil {
		return row
	}
	out := make(map[string]json.RawMessage, len(fields))
	for _, f := range fields {
		if v, ok := m[f]; ok {
			out[f] = v
		}
	}
	b, _ := json.Marshal(out)
	return b
}

// Rows collects row_to_json rows of a query.
func Rows(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]json.RawMessage, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var row json.RawMessage
		if err := rows.Scan(&row); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// RowsHandler answers a whole row_to_json query as a JSON array — the v1
// branches that read a view or a set-returning function with no paging.
func RowsHandler(d Doer, log *slog.Logger, sql string, args ...any) http.HandlerFunc {
	return RowsOf(d, log, sql, func(*http.Request) ([]any, error) { return args, nil })
}

// RowsOf is RowsHandler with the arguments decided from the request — its
// error is answered as is, before the database.
func RowsOf(d Doer, log *slog.Logger, sql string, args func(*http.Request) ([]any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := args(r)
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		var rows []json.RawMessage
		err = d.Do(r.Context(), platform.SessionOf(r), ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) (err error) {
			rows, err = Rows(ctx, tx, sql, a...)
			return err
		})
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		body, _ := json.Marshal(rows)
		WriteJSON(w, 200, body)
	}
}

// RowHandler answers one row of an api.* query keyed by the request — a
// lookup by something other than the id (a catalogue code, path elements):
// args decides the arguments (its error is answered as is), no row is 404,
// the row carries an ETag and honours If-None-Match as Get does.
func RowHandler(d Doer, log *slog.Logger, sql string, args func(*http.Request) ([]any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, err := args(r)
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		var row json.RawMessage
		req := ReqOf(r, 200, nil)
		if err := d.Do(r.Context(), platform.SessionOf(r), req, func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, sql, a...).Scan(&row); errors.Is(err, pgx.ErrNoRows) {
				return problem.New(404, "not-found", "Not found", "")
			} else if err != nil {
				return err
			}
			if tag := ETagOf(row); tag != "" && r.Header.Get("If-None-Match") == tag {
				req.Status = 304
			}
			return nil
		}); err != nil {
			Fail(w, r, log, err)
			return
		}
		SetETag(w, row)
		if req.Status == 304 {
			w.WriteHeader(304)
			return
		}
		WriteJSON(w, 200, row)
	}
}

// GetRow is one row of Get by id; no row is 404 — RLS does not distinguish
// "no such object" from "no rights", neither does the answer.
func (res Resource) GetRow(ctx context.Context, tx pgx.Tx, id any) (json.RawMessage, error) {
	cast := "$1::uuid"
	if res.IntID {
		cast = "$1::bigint"
	}
	var row json.RawMessage
	err := tx.QueryRow(ctx, "SELECT row_to_json(t) FROM "+res.GetFn+"("+cast+") t", id).Scan(&row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, problem.New(404, "not-found", "Not found", "")
	}
	return row, err
}

// List is GET <prefix>: ?filter/sort/fields/page → api.count_* + api.list_*,
// answered as {items, total, limit, offset}.
func (res Resource) List(d Doer, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params, err := query.Parse(r.URL.Query())
		if err != nil {
			Fail(w, r, log, problem.New(400, "validation", "Bad request", err.Error()))
			return
		}
		search, orderby, _ := params.JSONArgs()
		var items []json.RawMessage
		var total int64
		qp, _ := json.Marshal(map[string]any{"query": r.URL.RawQuery})
		err = d.Do(r.Context(), platform.SessionOf(r), ReqOf(r, 200, qp), func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, "SELECT "+res.CountFn+"($1)", search).Scan(&total); err != nil {
				return err
			}
			rows, err := Rows(ctx, tx, "SELECT row_to_json(t) FROM "+res.ListFn+"($1, NULL, $2, $3, $4) t", search, params.Limit, params.Offset, orderby)
			if err != nil {
				return err
			}
			for _, row := range rows {
				items = append(items, Project(row, params.Fields))
			}
			return nil
		})
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		if items == nil {
			items = []json.RawMessage{}
		}
		body, _ := json.Marshal(map[string]any{"items": items, "total": total, "limit": params.Limit, "offset": params.Offset})
		WriteJSON(w, 200, body)
	}
}

// Get is GET <prefix>/{id}: the row with ETag, 304 on If-None-Match.
func (res Resource) Get(d Doer, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := res.id(r)
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		var row json.RawMessage
		req := ReqOf(r, 200, nil)
		if err := d.Do(r.Context(), platform.SessionOf(r), req, func(ctx context.Context, tx pgx.Tx) error {
			if row, err = res.GetRow(ctx, tx, id); err != nil {
				return err
			}
			// decided here so that api.log_request records the status that goes on the wire
			if tag := ETagOf(row); tag != "" && r.Header.Get("If-None-Match") == tag {
				req.Status = 304
			}
			return nil
		}); err != nil {
			Fail(w, r, log, err)
			return
		}
		if tag := ETagOf(row); tag != "" {
			w.Header().Set("ETag", tag)
		}
		if req.Status == 304 {
			w.WriteHeader(304)
			return
		}
		WriteJSON(w, 200, row)
	}
}

// Precondition is the 428 of a PATCH without If-Match — decided before the
// body is read and before the database is touched.
func Precondition(r *http.Request) error {
	if r.Header.Get("If-Match") == "" {
		return problem.New(428, "precondition-required", "Precondition required", "PATCH needs If-Match with the ETag of the current revision")
	}
	return nil
}

// IfMatch compares If-Match with the current row inside the transaction:
// 412 when the row moved on since it was read.
func IfMatch(r *http.Request, current json.RawMessage) error {
	if err := Precondition(r); err != nil {
		return err
	}
	if tag := r.Header.Get("If-Match"); tag != "*" && ETagOf(current) != tag { // RFC 9110 §13.1.1: * is any current row
		return problem.New(412, "precondition-failed", "Precondition failed", "the object changed since it was read")
	}
	return nil
}

// ── Idempotency-Key ────────────────────────────────────────────────────

// IdemScope is the first half of an idempotency key: the user and the
// resource path — the same Idempotency-Key on two resources is two answers.
func IdemScope(r *http.Request, user string) string { return user + "\x00" + r.URL.Path }

// SetETag sets the ETag of a row when it has one.
func SetETag(w http.ResponseWriter, row json.RawMessage) {
	if tag := ETagOf(row); tag != "" {
		w.Header().Set("ETag", tag)
	}
}

// Idempotency stores the answer of a POST per (scope, key) for a TTL and
// replays it for the same body; a different body under the same key is 409.
// In the memory of the process — a shared store is a later change.
type Idempotency struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[string]*Stored
}

// Stored is one captured answer.
type Stored struct {
	hash   string
	status int
	header http.Header
	body   []byte
	at     time.Time
}

// NewIdempotency makes a store with the TTL.
func NewIdempotency(ttl time.Duration) *Idempotency {
	return &Idempotency{ttl: ttl, m: map[string]*Stored{}}
}

// Lookup returns the stored answer for (scope, key) with this body, or
// conflict=true when the key was used with another body.
func (i *Idempotency) Lookup(scope, key string, body []byte) (rec *Stored, conflict bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	s, ok := i.m[scope+"\x00"+key]
	if !ok || time.Since(s.at) > i.ttl {
		return nil, false
	}
	if s.hash != hashOf(body) {
		return nil, true
	}
	return s, false
}

// Store keeps what the Capture saw.
func (i *Idempotency) Store(scope, key string, body []byte, c *Capture) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.m) > 10000 { // crude bound; the shared store replaces this
		for k, v := range i.m {
			if time.Since(v.at) > i.ttl {
				delete(i.m, k)
			}
		}
	}
	i.m[scope+"\x00"+key] = &Stored{hash: hashOf(body), status: c.Status, header: c.Header().Clone(), body: c.body, at: time.Now()}
}

// Once runs a POST under its Idempotency-Key: with no key, or no store, it
// simply runs; the same key with the same body replays the stored answer;
// another body under the key is 409; a 5xx answer is not stored — the retry
// must reach the database. user scopes the key with the path (IdemScope).
func Once(w http.ResponseWriter, r *http.Request, idem *Idempotency, user string, raw []byte, log *slog.Logger, do func(http.ResponseWriter)) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" || idem == nil {
		do(w)
		return
	}
	scope := IdemScope(r, user)
	if rec, conflict := idem.Lookup(scope, key, raw); conflict {
		Fail(w, r, log, problem.New(409, "conflict", "Conflict", "Idempotency-Key reused with a different body"))
		return
	} else if rec != nil {
		rec.Replay(w)
		return
	}
	cw := &Capture{ResponseWriter: w}
	do(cw)
	if cw.Status < 500 {
		idem.Store(scope, key, raw, cw)
	}
}

// Replay writes the stored answer with Idempotency-Replayed: true.
func (s *Stored) Replay(w http.ResponseWriter) {
	for k, v := range s.header {
		w.Header()[k] = v
	}
	w.Header().Set("Idempotency-Replayed", "true")
	w.WriteHeader(s.status)
	_, _ = w.Write(s.body)
}

// Capture records status and body on the way to the client.
type Capture struct {
	http.ResponseWriter
	Status int
	body   []byte
}

// WriteHeader records the status.
func (c *Capture) WriteHeader(code int) { c.Status = code; c.ResponseWriter.WriteHeader(code) }

// Write records the body.
func (c *Capture) Write(b []byte) (int, error) {
	if c.Status == 0 {
		c.Status = 200
	}
	c.body = append(c.body, b...)
	return c.ResponseWriter.Write(b)
}
