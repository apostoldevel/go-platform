// Package object is the GoAPI package of the SQL module entity/object
// (db/sql/platform/entity/object): what every object has regardless of its
// entity — the row of api.object, its automaton (the methods of its state,
// an action or a method to run), and the full-text search over all of
// them. /api/v2/objects is the generic form of what an entity's own
// package answers under its prefix (/api/v2/clients/{id}/actions/…);
// /api/v2/search is v1 /search. The v1 routes were /action/execute,
// /method/run, /method/get and /search.
package object

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
	"github.com/jackc/pgx/v5"
)

// Doer runs the request transaction (pgtx.Runner in production).
type Doer = rest.Doer

// Config wires the package.
type Config struct {
	Doer   Doer
	Logger *slog.Logger
}

type module struct {
	cfg Config
	log *slog.Logger
}

const search = "/api/v2/search"

var objects = rest.Resource{Prefix: "/api/v2/objects", GetFn: "api.get_object", ListFn: "api.list_object", CountFn: "api.count_object"}

// codeRe is an action code or an entity code: a plain identifier.
var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// paramsOf reads the optional parameters of an action: a JSON object or
// nothing; anything else is 400. Returned as nil when absent.
func paramsOf(r *http.Request) (json.RawMessage, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, problem.New(400, "validation", "Bad request", "cannot read the body")
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, nil
	}
	if !json.Valid(raw) || raw[0] != '{' {
		return nil, problem.New(400, "validation", "Bad request", "the body must be a JSON object or empty")
	}
	return raw, nil
}

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger}
}

func (m *module) Name() string       { return "object" }
func (m *module) Prefixes() []string { return []string{objects.Prefix, search} }

// Routes registers the generic object and the search on the shared mux.
func (m *module) Routes(mux *http.ServeMux) {
	d, log := m.cfg.Doer, m.log
	p := objects.Prefix
	mux.HandleFunc("GET "+search, m.search)
	mux.HandleFunc("GET "+p, objects.List(d, log))
	mux.HandleFunc("GET "+p+"/{id}", objects.Get(d, log))
	mux.HandleFunc("GET "+p+"/{id}/methods", m.methods)
	mux.HandleFunc("POST "+p+"/{id}/actions/{action}", m.run("action", codeRe.MatchString, "SELECT api.execute_object_action($1::uuid, $2, $3::jsonb)"))
	mux.HandleFunc("POST "+p+"/{id}/methods/{method}", m.run("method", rest.IsUUID, "SELECT api.execute_method($1::uuid, $2::uuid, $3::jsonb)"))
}

// search is GET /search?q=&entities=a,b: api.search in the session's locale
// (or ?locale=), the matching objects as an array — v1 has no paging here.
func (m *module) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	text := strings.TrimSpace(q.Get("q"))
	if text == "" {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "q is required"))
		return
	}
	var entities []string
	for _, e := range strings.Split(q.Get("entities"), ",") {
		if e = strings.TrimSpace(e); e == "" {
			continue
		}
		if !codeRe.MatchString(e) {
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "entities must be a comma-separated list of entity codes"))
			return
		}
		entities = append(entities, e)
	}
	var entitiesJSON []byte
	if len(entities) > 0 {
		entitiesJSON, _ = json.Marshal(entities)
	}
	var locale *string
	if l := q.Get("locale"); l != "" {
		locale = &l
	}
	rest.RowsHandler(m.cfg.Doer, m.log, "SELECT row_to_json(t) FROM api.search($1, $2::jsonb, coalesce($3, (SELECT code FROM api.current_locale()))) t", text, entitiesJSON, locale)(w, r)
}

// methods is GET /objects/{id}/methods: the methods of the object's state
// the caller may see, in sequence — the buttons of its card.
func (m *module) methods(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	var items []json.RawMessage
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := objects.GetRow(ctx, tx, id); err != nil {
			return err
		}
		items, err = rest.Rows(ctx, tx, "SELECT row_to_json(t) FROM api.get_object_methods($1::uuid) t ORDER BY t.sequence", id)
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	body, _ := json.Marshal(items)
	rest.WriteJSON(w, 200, body)
}

// run is POST /objects/{id}/actions/{action} or /methods/{method}: the
// object must be visible (404), the action runs with the body as params
// in the one transaction, and the object's row after it is the answer.
func (m *module) run(what string, valid func(string) bool, sql string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := rest.IDOf(r)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		key := r.PathValue(what)
		if !valid(key) {
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "bad "+what))
			return
		}
		params, err := paramsOf(r)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		var row json.RawMessage
		err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, params), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := objects.GetRow(ctx, tx, id); err != nil {
				return err
			}
			var p any
			if params != nil {
				p = params
			}
			if _, err := tx.Exec(ctx, sql, id, key, p); err != nil {
				return err
			}
			row, err = objects.GetRow(ctx, tx, id)
			return err
		})
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		rest.SetETag(w, row)
		rest.WriteJSON(w, 200, row)
	}
}
