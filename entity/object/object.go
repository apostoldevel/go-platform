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
	"github.com/apostoldevel/go-platform/lib/pgtx"
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
	mux.HandleFunc("POST "+p+"/{id}/actions/{action}", m.run("action", codeRe.MatchString, "execute_object_action", "code"))
	mux.HandleFunc("POST "+p+"/{id}/methods/{method}", m.run("method", rest.IsUUID, "execute_method", "method"))
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
	_, err := pgtx.Call(ctx, tx, "chmodo", pgtx.Args{"object": id, "mask": *b.Mask, "userid": b.UserID})
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
		if err := pgtx.CallScalar(ctx, tx, "count_object_file", pgtx.Args{"search": pgtx.JSON(search)}, &total); err != nil {
			return err
		}
		rows, err := pgtx.Call(ctx, tx, "list_object_file", pgtx.Args{"search": pgtx.JSON(search), "limit": params.Limit, "offset": params.Offset, "orderby": pgtx.JSON(orderby)})
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
		rows, err = pgtx.Call(ctx, tx, "set_object_files_json", pgtx.Args{"id": id, "files": pgtx.JSON(raw)})
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
		var err error
		row, err = pgtx.CallRow(ctx, tx, "get_object_file", pgtx.Args{"object": id, "file": file, "name": nil})
		if errors.Is(err, pgtx.ErrNoRow) {
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
		if err := pgtx.CallScalar(ctx, tx, "delete_object_file", pgtx.Args{"object": id, "file": file, "name": nil}, &deleted); err != nil {
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
		_, err := pgtx.Call(ctx, tx, "clear_object_files", pgtx.Args{"id": id})
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
	args := pgtx.Args{"text": text, "entities": pgtx.JSON(entitiesJSON)}
	// no locale: the key is left out, and pLocaleCode's DEFAULT is the session's locale_code()
	if l := q.Get("locale"); l != "" {
		args["localecode"] = l
	}
	rest.CallRowsHandler(m.cfg.Doer, m.log, "search", args)(w, r)
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
		// api.get_object_methods ends in ORDER BY sequence, and the call keeps the order
		items, err = pgtx.Call(ctx, tx, "get_object_methods", pgtx.Args{"object": id})
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
// run calls fn(pObject, p<key>, pParams): execute_object_action by the
// action's code, execute_method by the method's id — the parameter names
// pick the overload (pCode / pMethod), not a cast.
func (m *module) run(what string, valid func(string) bool, fn, key string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := rest.IDOf(r)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		argKey := key
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
			if _, err := pgtx.Call(ctx, tx, fn, pgtx.Args{"object": id, argKey: key, "params": pgtx.JSON(params)}); err != nil {
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
