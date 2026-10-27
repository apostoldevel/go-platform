// Package entity is the declarative shape of a GoAPI entity package over its
// api.* — a thin, stateless mirror where every request is one transaction
// (pgtx), access is the database's (Access<X> through api.sql('kernel', …)
// and api.<x>), every error is problem+json and the row is whatever api.<x>
// returns — nothing here adds a field. It lives in the platform library so
// that a platform entity and a project's entity are mounted by one form.
//
// It is the project's own layer over go-platform's lib/rest, for what
// lib/rest does not do (yet) and this configuration needs:
//
//   - GET <xs> takes q= beside filter[…]: one term matched over several
//     columns with OR — the operator language of api.sql() has it, lib/query's
//     URL form does not (search.go).
//   - A query parameter a list route does not know is 400, while lib/query
//     refuses only the unknown names made of a-z (JSON:API reserves them) and
//     ignores the rest: the keys are checked before Parse (KnownKeys); q= is
//     taken only by an entity with SearchFields, once (listArgs); a repeated
//     q= is 400. GET <xs>/{id} takes `fields` as v1's /<x>/get does and
//     nothing else — any other key is 400, as in a list; the body is cut,
//     the ETag stays the whole row's (getFields). An unknown key, in a body or
//     in a query, carries ERR-400-061 and a missing body ERR-400-060 — the
//     codes of v1 for the same refusals.
//   - api.get_<x> must answer an unknown or invisible id with no row, and
//     no row is 404. A function that RETURNS SETOF api.<x> does; one that
//     RETURNS a bare composite answers a missing id with one row of NULLs — read as 404 here by the null id, so the form
//     holds for both shapes of the catalogue.
//   - PATCH refuses an empty body: the call would be an empty edit that
//     still moves udate and writes "<X> updated".
//   - POST <xs>/{id}/actions/{action} and GET <xs>/{id}/methods — the
//     workflow of the entity, on a visible row.
//   - DELETE is not one of the six routes of the form (405 with Allow from
//     the host where nothing registers it): where an entity has a deletion
//     with v1 parity (execute_object_action 'delete'), the package registers
//     it as its own route beside these.
//
// A package names its api.* and its body (Entity), takes the six routes from
// Module.Routes and adds what is not a template (a /history, a DELETE) with
// RowsOf.
package entity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/query"
	"github.com/apostoldevel/go-platform/lib/rest"
	"github.com/jackc/pgx/v5"
)

// Entity names one SQL entity for the Go side: its module name (the SQL
// entity's, `invoice`), the prefix registered with the gateway, the api.*
// family, the api.set_<x> it writes with (by name — the body's keys are the
// parameters' names), the body decoded into it, and the columns q= is
// matched against.
type Entity struct {
	Name         string        // "invoice" — the SQL entity's name, the module's Name()
	Prefix       string        // "/api/v2/invoices"
	Resource     rest.Resource // api.get_/list_/count_<x>
	SetFn        string        // "api.set_<x>" — called with Body.Args
	UpdateFn     string        // when set, PATCH calls it instead of SetFn (a catalogue whose api.set_<x> coalesces what api.update_<x> keeps: a PATCH must move only what it was given)
	NewBody      func() rest.Body
	SearchFields []string // columns of api.<x> one term of q= is matched against
	// Defaults, when set, fills on create what the database will not accept
	// as NULL (rest.Writable.Defaults); PATCH never calls it.
	Defaults func(rest.Body)
	// CreateFn, when set, is the create of POST instead of SetFn(NULL, …) —
	// for an entity whose api.set_<x>(NULL, …) is not a create but an upsert
	// by code (a family where pId := coalesce(pId, Get<X>(pCode))):
	// "api.add_<x>", called with Body.Args(nil) less the id and less
	// CreateOmit (keys it has no parameter for, refused on create by
	// Validate), then api.get_<x> of the new id (rest.Writable.CreateFn).
	CreateFn   string
	CreateOmit []string
	// CreateExtra, when set, names parameters of CreateFn the body
	// deliberately does not carry — coalesced by the catalogue on create
	// (a tenant derived from the parent object). Declared so the
	// write-signature grid knows the gap is a decision, not a drift.
	CreateExtra []string
	// Row, when set, is the zero value of a struct that names the columns of
	// api.<x> with their json, doc and format tags (openapi.Schema). It makes
	// the entity described in the OpenAPI document (Operations); without it
	// the entity is not, since a row that is raw JSON has no type to
	// describe. The struct must carry the columns the database really gives —
	// the parity tool (-spec) holds the document to real rows.
	Row any
}

// Config wires a package; JWT, request id and in-flight accounting are the
// host's (platform.New), not the package's.
type Config struct {
	Doer   rest.Doer
	Logger *slog.Logger
}

// Module is the platform.Module of one entity: the six routes of the form,
// and the handlers a package composes its own routes from.
type Module struct {
	Entity
	Doer     rest.Doer
	Log      *slog.Logger
	Idem     *rest.Idempotency
	writable rest.Writable
	creator  rest.Writable
	methods  http.HandlerFunc
	get      http.HandlerFunc
}

// New wires the entity.
func New(e Entity, cfg Config) *Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	m := &Module{Entity: e, Doer: cfg.Doer, Log: cfg.Logger, Idem: rest.NewIdempotency(24 * time.Hour)}
	m.writable = rest.Writable{Resource: e.Resource, SetFn: e.SetFn, NewBody: e.NewBody, Defaults: e.Defaults}
	m.creator = m.writable
	m.creator.CreateFn, m.creator.CreateOmit = e.CreateFn, e.CreateOmit
	// every body must name its positional list in full: a wrong count is a
	// panic of entity.Named — here, at start, not on every write
	func() {
		defer func() {
			if r := recover(); r != nil {
				panic(fmt.Sprintf("entity %s: %v", e.Name, r))
			}
		}()
		e.NewBody().Args(nil)
	}()
	m.methods = m.RowsOf("get_object_methods", "object") // the function ends in ORDER BY sequence
	m.get = rest.RowFieldsFunc(m.Doer, m.Log, idArg, m.getRow, fieldsOf)
	return m
}

// Name is the SQL entity's name.
func (m *Module) Name() string { return m.Entity.Name }

// Shape is the entity the module was wired with. The grid of the tests reads
// the write through it — the body, SetFn and CreateFn of every package the
// binary mounts, checked against the catalogue — so that a package added to
// modules() is under the check without being listed anywhere else.
func (m *Module) Shape() Entity { return m.Entity }

// Shaped is a platform.Module wired by New, directly or embedded.
type Shaped interface{ Shape() Entity }

// Prefixes is what the package registers with the gateway.
func (m *Module) Prefixes() []string { return []string{m.Prefix} }

// Routes registers the six routes of the form on the shared mux. A package
// with routes of its own (a DELETE, a /history) registers them beside these
// in its own Routes.
func (m *Module) Routes(mux *http.ServeMux) {
	// the table is the description's too (openapi.go): what is mounted and
	// what Operations says of it come from the same six entries
	for _, r := range m.form() {
		mux.HandleFunc(r.op.Pattern(), r.h)
	}
}

// getRow is the read behind GET <xs>/{id}: the row of api.get_<x>, with no
// row — or a row that is NULLs — read as 404. A get of the catalogue's
// SETOF shape answers a missing id with no row (pgtx.ErrNoRow); one of the
// bare-composite shape with a single row whose every
// field is NULL, id first — a null id is no row by any other spelling.
func (m *Module) getRow(ctx context.Context, tx pgx.Tx, a pgtx.Args) (json.RawMessage, error) {
	row, err := pgtx.CallRow(ctx, tx, m.Resource.GetFn, a)
	if err != nil {
		return nil, err
	}
	var probe struct {
		ID any `json:"id"`
	}
	if err := json.Unmarshal(row, &probe); err == nil && probe.ID == nil {
		return nil, pgtx.ErrNoRow
	}
	return row, nil
}

func idArg(r *http.Request) (pgtx.Args, error) {
	id, err := rest.IDOf(r)
	if err != nil {
		return nil, err
	}
	return pgtx.Args{"id": id}, nil
}

// KnownKeys refuses a query parameter the route does not take: an unknown
// top-level key is a typo ("limit=10" of "page[limit]=10"), not a request to
// ignore (the list language of a-z names is reserved by JSON:API). plain are the bare names the route takes beside filter[…] and page[…].
func KnownKeys(values url.Values, plain ...string) error {
	for k := range values {
		if strings.HasPrefix(k, "filter[") || strings.HasPrefix(k, "page[") {
			continue
		}
		known := false
		for _, p := range plain {
			known = known || k == p
		}
		if !known {
			return problem.New(400, "validation", "Bad request", "unknown parameter "+k).WithCode(problem.CodeJSONKey)
		}
	}
	return nil
}

// listArgs is the query of GET <xs> checked and translated: the keys the
// route takes (KnownKeys; q= only with searchFields), then lib/query's Parse
// and the search of SearchOf. q= is this layer's term search, not a name of
// the list language: lib/query refuses an unknown name made of a-z (JSON:API
// reserves them), so q= is read first and left out of what Parse sees. A
// repeated q= is ambiguous — 400. values is not changed. Every error is a 400
// problem.
func listArgs(values url.Values, searchFields []string) (query.Params, []byte, error) {
	plain := []string{"sort", "fields"}
	if len(searchFields) > 0 {
		plain = append(plain, "q")
	}
	if err := KnownKeys(values, plain...); err != nil {
		return query.Params{}, nil, err
	}
	if len(values["q"]) > 1 {
		return query.Params{}, nil, problem.New(400, "validation", "Bad request", "q is given more than once")
	}
	term := values.Get("q")
	list := maps.Clone(values)
	delete(list, "q")
	params, err := query.Parse(list)
	if err != nil {
		return params, nil, problem.New(400, "validation", "Bad request", err.Error())
	}
	return params, SearchOf(params, term, searchFields), nil
}

// List is GET <xs>: filter[…]/sort/fields/page as lib/query translates
// them, plus q= — answered as {items, total, limit, offset}, total from
// api.count_<x> over the same search.
func (m *Module) List(w http.ResponseWriter, r *http.Request) {
	params, search, err := listArgs(r.URL.Query(), m.SearchFields)
	if err != nil {
		rest.Fail(w, r, m.Log, err)
		return
	}
	_, orderby, _ := params.JSONArgs()
	var items []json.RawMessage
	var total int64
	qp, _ := json.Marshal(map[string]any{"query": r.URL.RawQuery})
	err = m.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, qp), func(ctx context.Context, tx pgx.Tx) error {
		if err := pgtx.CallScalar(ctx, tx, m.Resource.CountFn, pgtx.Args{"search": pgtx.JSON(search)}, &total); err != nil {
			return err
		}
		rows, err := pgtx.Call(ctx, tx, m.Resource.ListFn, pgtx.Args{"search": pgtx.JSON(search), "limit": params.Limit, "offset": params.Offset, "orderby": pgtx.JSON(orderby)})
		if err != nil {
			return err
		}
		for _, row := range rows {
			items = append(items, rest.Project(row, params.Fields))
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.Log, err)
		return
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	body, _ := json.Marshal(map[string]any{"items": items, "total": total, "limit": params.Limit, "offset": params.Offset})
	rest.WriteJSON(w, 200, body)
}

// fieldsOf is the query of a route that takes `fields` and nothing else
// (GET <xs>/{id}, the sets of RowsOf): any other key is 400, the names are
// lib/query's. None named is nil — the whole row.
func fieldsOf(r *http.Request) ([]string, error) {
	values := r.URL.Query()
	for k := range values {
		if k != "fields" { // not even filter[…] and page[…], which KnownKeys lets through
			return nil, problem.New(400, "validation", "Bad request", "only fields= is taken here, not "+k).WithCode(problem.CodeJSONKey)
		}
	}
	p, err := query.Parse(values)
	if err != nil {
		return nil, problem.New(400, "validation", "Bad request", err.Error())
	}
	return p.Fields, nil
}

// Current is the visible row of {id} inside the transaction, 404 otherwise
// (a get of a missing id answers no row — pgtx.ErrNoRow from the SETOF
// shape, or read off the null id of the bare-composite's row of NULLs —
// and no row is the caller's 404, never the pool's 500).
func (m *Module) Current(ctx context.Context, tx pgx.Tx, id string) (json.RawMessage, error) {
	row, err := m.getRow(ctx, tx, pgtx.Args{"id": id})
	if errors.Is(err, pgtx.ErrNoRow) {
		return nil, problem.New(404, "not-found", "Not found", "")
	}
	return row, err
}

// emptyBody is a body with no key at all (or every key null): nothing to
// set — every argument after the id is nil.
func emptyBody(b rest.Body) bool {
	for k, a := range b.Args(nil) {
		if k != "id" && a != nil {
			return false
		}
	}
	return true
}

// Update is PATCH <xs>/{id}: id, then If-Match (428 without, no body read),
// then the body — at least one key, or the call is an empty edit that still
// moves udate; in the transaction the current row (404), the precondition
// (412), api.set_<x>(id, …), the new row with its ETag.
func (m *Module) Update(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err == nil {
		err = rest.Precondition(r)
	}
	if err != nil {
		rest.Fail(w, r, m.Log, err)
		return
	}
	b := m.NewBody()
	raw, err := rest.ReadBody(r, b)
	if err == nil && emptyBody(b) {
		empty := problem.New(400, "validation", "Bad request", "body must carry at least one field")
		if len(raw) == 0 {
			empty = empty.WithCode(problem.CodeJSONEmpty)
		}
		err = empty
	}
	if err == nil {
		err = b.Validate(false)
	}
	if err != nil {
		rest.Fail(w, r, m.Log, err)
		return
	}
	var row json.RawMessage
	err = m.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, raw), func(ctx context.Context, tx pgx.Tx) error {
		cur, err := m.Current(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := rest.IfMatch(r, cur); err != nil {
			return err
		}
		if m.UpdateFn != "" {
			// the update of the catalogue is void: the answer is a re-read,
			// not the call's return
			if _, err := pgtx.Call(ctx, tx, m.UpdateFn, b.Args(id)); err != nil {
				return err
			}
			row, err = m.Current(ctx, tx, id)
			return err
		}
		row, err = pgtx.CallRow(ctx, tx, m.SetFn, b.Args(id))
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.Log, err)
		return
	}
	rest.SetETag(w, row)
	rest.WriteJSON(w, 200, row)
}

var actionRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Action is POST <xs>/{id}/actions/{action}: the workflow's
// api.execute_object_action by code, on a visible row (404 otherwise); the
// body, when present, is the action's params; the answer is the row after.
func (m *Module) Action(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.Log, err)
		return
	}
	action := r.PathValue("action")
	if !actionRe.MatchString(action) {
		rest.Fail(w, r, m.Log, problem.New(400, "validation", "Bad request", "bad action name"))
		return
	}
	params, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		rest.Fail(w, r, m.Log, problem.New(413, "validation", "Payload too large", "body over 1 MiB"))
		return
	case err != nil, len(params) > 0 && (!json.Valid(params) || bytes.TrimLeft(params, " \t\r\n")[0] != '{'):
		rest.Fail(w, r, m.Log, problem.New(400, "validation", "Bad request", "body must be a JSON object or empty"))
		return
	}
	var row json.RawMessage
	req := rest.ReqOf(r, 200, params)
	err = m.Doer.Do(r.Context(), platform.SessionOf(r), req, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := m.Current(ctx, tx, id); err != nil {
			return err
		}
		if _, err := pgtx.Call(ctx, tx, "execute_object_action", pgtx.Args{"object": id, "code": action, "params": pgtx.JSON(params)}); err != nil {
			return err
		}
		// the row after the action is the answer; drop leaves none — 404 on
		// the re-read is exactly that, and the answer is 204, decided here so
		// api.log_request records the status on the wire
		var err error
		if row, err = m.Current(ctx, tx, id); err != nil {
			var p *problem.Problem
			if !errors.As(err, &p) || p.Status != 404 {
				return err
			}
			row, req.Status = nil, 204
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.Log, err)
		return
	}
	if req.Status == 204 {
		w.WriteHeader(204)
		return
	}
	rest.SetETag(w, row)
	rest.WriteJSON(w, 200, row)
}

// Get is GET <xs>/{id}: the visible row (404 otherwise) with its ETag, honouring
// If-None-Match; `fields` cuts the body and nothing else is taken (fieldsOf).
func (m *Module) Get(w http.ResponseWriter, r *http.Request) { m.get(w, r) }

// Methods is GET <xs>/{id}/methods: api.get_object_methods of a visible
// row, in the order the workflow gives them.
func (m *Module) Methods(w http.ResponseWriter, r *http.Request) { m.methods(w, r) }

// RowsOf answers a set keyed by {id} as a JSON array — fn called with the
// id under key — after the row itself was found visible in the same
// transaction (404 otherwise). The set is whole and in the function's order:
// only fields= is taken, a sort/filter/page would be validated and then
// ignored — refused instead.
func (m *Module) RowsOf(fn, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := rest.IDOf(r)
		if err != nil {
			rest.Fail(w, r, m.Log, err)
			return
		}
		fields, err := fieldsOf(r)
		if err != nil {
			rest.Fail(w, r, m.Log, err)
			return
		}
		var items []json.RawMessage
		err = m.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := m.Current(ctx, tx, id); err != nil {
				return err
			}
			rows, err := pgtx.Call(ctx, tx, fn, pgtx.Args{key: id})
			if err != nil {
				return err
			}
			for _, row := range rows {
				items = append(items, rest.Project(row, fields))
			}
			return nil
		})
		if err != nil {
			rest.Fail(w, r, m.Log, err)
			return
		}
		if items == nil {
			items = []json.RawMessage{}
		}
		body, _ := json.Marshal(items)
		rest.WriteJSON(w, 200, body)
	}
}

// Named are the named arguments of api.set_<x> from a body's positional
// ones: position 0 is the id, position i the body's i-th json key in the
// order of its fields, embedded structs flattened — the keys are the
// parameters' names without the p (the order and the names are pinned to
// the catalogue). A count that does not match is a programming error —
// New calls every body once at start, so it is found there. Every field of a
// body carries a json tag (the grid refuses one without).
func Named(body any, positional []any) pgtx.Args {
	keys := append([]string{"id"}, jsonKeys(reflect.TypeOf(body))...)
	if len(keys) != len(positional) {
		panic(fmt.Sprintf("entity.Named: %T has the id and %d keys, positional gives %d", body, len(keys)-1, len(positional)))
	}
	out := make(pgtx.Args, len(keys))
	for i, k := range keys {
		out[k] = positional[i]
	}
	return out
}

// jsonKeys are the json keys of a body struct in field order, embedded
// structs flattened.
func jsonKeys(t reflect.Type) []string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, jsonKeys(f.Type)...)
			continue
		}
		key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if key != "" && key != "-" {
			out = append(out, key)
		}
	}
	return out
}
