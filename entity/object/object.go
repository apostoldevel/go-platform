// Package object is the GoAPI package of the SQL module entity/object
// (db/sql/platform/entity/object): what every object has regardless of its
// entity — the row of api.object, its automaton (the methods of its state,
// an action or a method to run), its access (AOU: who holds what, one
// grant, the bits of one user decoded), its files, and the full-text
// search over all of them. /api/v2/objects is the generic form of what an
// entity's own package answers under its prefix
// (/api/v2/clients/{id}/actions/…); /api/v2/search is v1 /search. The v1
// routes were /action/execute, /method/run, /method/get, /search and, of
// the platform's own dispatcher rest.object registered outside InitAPI(),
// /object/access, /object/access/set, /object/access/decode and
// /object/file, /object/file/{set,get,list,delete,clear}. The rest of
// rest.object (class, type, state and method history, groups, links,
// data, addresses, geolocation) has no consumer on either side and no
// form here yet.
package object

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/query"
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
	rest.Access{Resource: objects, ListFn: "api.object_access", DecodeFn: "api.decode_object_access", MaxMask: rest.MaskBits6, Set: chmodo}.Routes(mux, d, log)
	mux.HandleFunc("GET "+p+"/{id}/files", m.files)
	mux.HandleFunc("POST "+p+"/{id}/files", m.filesAdd)
	mux.HandleFunc("DELETE "+p+"/{id}/files", m.filesClear)
	mux.HandleFunc("GET "+p+"/{id}/files/{file}", m.file)
	mux.HandleFunc("DELETE "+p+"/{id}/files/{file}", m.fileDelete)
}

// chmodo is one grant on an object (AOU); api.chmodo decides who may. The
// mask is six bits (deny/allow × s/u/d) — rest.Access bounds it before the
// database, which would cast a wider number to bit(6) and wrap.
func chmodo(ctx context.Context, tx pgx.Tx, id string, b rest.AccessBody) error {
	_, err := tx.Exec(ctx, "SELECT api.chmodo($1::uuid, $2::int, $3::uuid)", id, *b.Mask, b.UserID)
	return err
}

// ── files: what is attached to an object (api.object_file) ─────────────

// files is GET /objects/{id}/files: the object's files as a list with
// total and paging, ?filter/sort/fields on top of `object = id` — v1
// /object/file/list with {filter: {object}}.
func (m *module) files(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	params, err := query.Parse(r.URL.Query())
	if err != nil {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", err.Error()))
		return
	}
	params.Search = append([]query.Condition{{Field: "object", Compare: "EQL", Value: id}}, params.Search...)
	search, orderby, _ := params.JSONArgs()
	var items []json.RawMessage
	var total int64
	qp, _ := json.Marshal(map[string]any{"query": r.URL.RawQuery})
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, qp), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := objects.GetRow(ctx, tx, id); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT api.count_object_file($1)", search).Scan(&total); err != nil {
			return err
		}
		rows, err := rest.Rows(ctx, tx, "SELECT row_to_json(t) FROM api.list_object_file($1, NULL, $2, $3, $4) t", search, params.Limit, params.Offset, orderby)
		if err != nil {
			return err
		}
		for _, row := range rows {
			items = append(items, rest.Project(row, params.Fields))
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	if items == nil {
		items = []json.RawMessage{}
	}
	body, _ := json.Marshal(map[string]any{"items": items, "total": total, "limit": params.Limit, "offset": params.Offset})
	rest.WriteJSON(w, 200, body)
}

// MaxFilesBody bounds the body of POST /objects/{id}/files: base64 bytes
// of files, not a form.
const MaxFilesBody = 64 << 20

// fileBody is one file as api.set_object_files_json takes it. The file id
// is deliberately not accepted: the database attaches an existing
// db.file to the object by id without asking whose it is (NewObjectFile,
// ON CONFLICT DO NOTHING), and a replace is resolved by name and path
// anyway. A negative size is a delete in the database (SetObjectFile) —
// the delete form is DELETE.
type fileBody struct {
	Name *string `json:"name"`
	Path *string `json:"path"`
	Size *int32  `json:"size"`
	Date *string `json:"date"`
	Data *string `json:"data"`
	Hash *string `json:"hash"`
	Text *string `json:"text"`
	Type *string `json:"type"`
}

// filesOf reads the body: a non-empty JSON array of files, every element an
// object of the known keys, name required, size not negative — the 400s
// decided before the database; the raw body is what goes to it.
func filesOf(r *http.Request) ([]byte, []fileBody, error) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, MaxFilesBody))
	if err != nil {
		return nil, nil, problem.New(400, "validation", "Bad request", "cannot read the body")
	}
	var files []*fileBody
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&files); err != nil || dec.More() || len(files) == 0 {
		return nil, nil, problem.New(400, "validation", "Bad request", "the body must be a non-empty JSON array of files {name, path, size, date, data, hash, text, type}")
	}
	out := make([]fileBody, len(files))
	for i, f := range files {
		switch {
		case f == nil:
			return nil, nil, problem.New(400, "validation", "Bad request", "files["+strconv.Itoa(i)+"]: not an object")
		case f.Name == nil || strings.TrimSpace(*f.Name) == "":
			return nil, nil, problem.New(400, "validation", "Bad request", "files["+strconv.Itoa(i)+"]: name is required")
		case f.Size != nil && *f.Size < 0:
			return nil, nil, problem.New(400, "validation", "Bad request", "files["+strconv.Itoa(i)+"]: size must not be negative")
		}
		out[i] = *f
	}
	return raw, out, nil
}

// filesAdd is POST /objects/{id}/files: the body is a JSON array of files
// ({name, path, size, date, data (base64), hash, text, type}; an existing
// name at the path is replaced) — api.set_object_files_json, the rows
// written back. An upsert answers 200, as v1 /object/file/set {id, files}
// did; its `clear` is DELETE …/files first.
func (m *module) filesAdd(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	raw, files, err := filesOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	var rows []json.RawMessage
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, withoutData(files)), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := objects.GetRow(ctx, tx, id); err != nil {
			return err
		}
		rows, err = rest.Rows(ctx, tx, "SELECT row_to_json(t) FROM api.set_object_files_json($1::uuid, $2::json) t", id, raw)
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	body, _ := json.Marshal(rows)
	rest.WriteJSON(w, 200, body)
}

// withoutData is the request as journalled — {"files": [metadata…]}, not
// the wire's bare array and not the bytes: db.api_log is an audit, not a
// store.
func withoutData(files []fileBody) []byte {
	meta := make([]fileBody, len(files))
	for i, f := range files {
		f.Data = nil
		meta[i] = f
	}
	out, _ := json.Marshal(map[string]any{"files": meta})
	return out
}

// file is GET /objects/{id}/files/{file}: one file with its bytes
// (api.object_file_data: `data` base64) — v1 /object/file/get.
func (m *module) file(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	file := r.PathValue("file")
	if !rest.IsUUID(file) {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "bad file id"))
		return
	}
	var row json.RawMessage
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := objects.GetRow(ctx, tx, id); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, "SELECT row_to_json(t) FROM api.get_object_file($1::uuid, $2::uuid, NULL, NULL) t", id, file).Scan(&row)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem.New(404, "not-found", "Not found", "")
		}
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	rest.WriteJSON(w, 200, row)
}

// fileDelete is DELETE /objects/{id}/files/{file}: 204, or 404 when the
// object has no such file — v1 /object/file/delete {id, file}.
func (m *module) fileDelete(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	file := r.PathValue("file")
	if !rest.IsUUID(file) {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "bad file id"))
		return
	}
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 204, nil), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := objects.GetRow(ctx, tx, id); err != nil {
			return err
		}
		var deleted bool
		if err := tx.QueryRow(ctx, "SELECT api.delete_object_file($1::uuid, $2::uuid, NULL, NULL)", id, file).Scan(&deleted); err != nil {
			return err
		}
		if !deleted {
			return problem.New(404, "not-found", "Not found", "")
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	w.WriteHeader(204)
}

// filesClear is DELETE /objects/{id}/files: every file of the object goes
// (api.clear_object_files), 204 — v1 /object/file/clear and the `clear`
// flag of /object/file/set.
func (m *module) filesClear(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 204, nil), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := objects.GetRow(ctx, tx, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "SELECT api.clear_object_files($1::uuid)", id)
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	w.WriteHeader(204)
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
