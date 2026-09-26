// Package registry is the GoAPI package of the SQL module registry
// (db/sql/platform/registry): the tree of keys and typed values (a
// Windows-registry-like store, roots CURRENT_CONFIG and CURRENT_USER) as
// /api/v2/registry — v1 rest.registry. Values are typed in the body
// ({type, value}), not in the path as v1's /registry/write/<type>; a key
// is addressed as (key, subkey) or by id, the way the database's functions
// take it.
package registry

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
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

// Prefix is the one prefix of the package.
const Prefix = "/api/v2/registry"

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger}
}

func (m *module) Name() string       { return "registry" }
func (m *module) Prefixes() []string { return []string{Prefix} }

// Routes registers the registry on the shared mux.
func (m *module) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+Prefix, m.list)                      // api.registry[_ex](id, key, subkey)
	mux.HandleFunc("GET "+Prefix+"/keys", m.keys)              // api.registry_key(id, root, parent, key)
	mux.HandleFunc("GET "+Prefix+"/keys/{id}/path", m.keyPath) // api.registry_get_reg_key(id)
	mux.HandleFunc("GET "+Prefix+"/keys/enum", m.enumKeys)     // api.registry_enum_key(key, subkey)
	mux.HandleFunc("GET "+Prefix+"/values", m.enumValues)      // api.registry_enum_value[_ex](key, subkey)
	mux.HandleFunc("GET "+Prefix+"/values/read", m.read)       // api.registry_read(key, subkey, name)
	mux.HandleFunc("PUT "+Prefix+"/values", m.write)           // api.registry_write(id, key, subkey, name, type, value)
	mux.HandleFunc("DELETE "+Prefix+"/values/{id}", m.deleteValueByID)
	mux.HandleFunc("DELETE "+Prefix+"/values", m.deleteValue) // ?key=&subkey=&name=
	mux.HandleFunc("DELETE "+Prefix+"/keys", m.deleteKey)     // ?key=&subkey=
	mux.HandleFunc("DELETE "+Prefix+"/tree", m.deleteTree)    // ?key=&subkey=
}

// optional query parameters, typed for the database
func uuidParam(q url.Values, name string) (*string, error) {
	v := q.Get(name)
	if v == "" {
		return nil, nil
	}
	if !rest.IsUUID(v) {
		return nil, problem.New(400, "validation", "Bad request", name+" must be a UUID")
	}
	return &v, nil
}

func textParam(q url.Values, name string) *string {
	if v := q.Get(name); v != "" {
		return &v
	}
	return nil
}

func (m *module) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, err := uuidParam(q, "id")
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	key, err := uuidParam(q, "key")
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	subkey, err := uuidParam(q, "subkey")
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	fn := "api.registry"
	if q.Get("extended") == "true" {
		fn = "api.registry_ex"
	}
	rest.CallRowsHandler(m.cfg.Doer, m.log, fn, pgtx.Args{"id": id, "key": key, "subkey": subkey})(w, r)
}

func (m *module) keys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var ids [3]*string
	for i, name := range []string{"id", "root", "parent"} {
		v, err := uuidParam(q, name)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		ids[i] = v
	}
	rest.CallRowsHandler(m.cfg.Doer, m.log, "registry_key", pgtx.Args{"id": ids[0], "root": ids[1], "parent": ids[2], "key": textParam(q, "key")})(w, r)
}

func (m *module) keyPath(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	var path *string
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		return pgtx.CallScalar(ctx, tx, "registry_get_reg_key", pgtx.Args{"key": id}, &path)
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	if path == nil {
		rest.Fail(w, r, m.log, problem.New(404, "not-found", "Not found", ""))
		return
	}
	body, _ := json.Marshal(map[string]string{"id": id, "path": *path})
	rest.WriteJSON(w, 200, body)
}

// keyRef is (key, subkey) of the enum/delete functions; key is the root alias.
func keyRef(q url.Values) (string, *string, error) {
	key := q.Get("key")
	if key == "" {
		return "", nil, problem.New(400, "validation", "Bad request", "key is required (CURRENT_CONFIG or CURRENT_USER)")
	}
	return key, textParam(q, "subkey"), nil
}

func (m *module) enumKeys(w http.ResponseWriter, r *http.Request) {
	key, subkey, err := keyRef(r.URL.Query())
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	rest.CallRowsHandler(m.cfg.Doer, m.log, "registry_enum_key", pgtx.Args{"key": key, "subkey": subkey})(w, r)
}

func (m *module) enumValues(w http.ResponseWriter, r *http.Request) {
	key, subkey, err := keyRef(r.URL.Query())
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	fn := "api.registry_enum_value"
	if r.URL.Query().Get("extended") == "true" {
		fn = "api.registry_enum_value_ex"
	}
	rest.CallRowsHandler(m.cfg.Doer, m.log, fn, pgtx.Args{"key": key, "subkey": subkey})(w, r)
}

func (m *module) read(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key, subkey, err := keyRef(q)
	if err == nil && q.Get("name") == "" {
		err = problem.New(400, "validation", "Bad request", "name is required")
	}
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	var row json.RawMessage
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		row, err = pgtx.CallRow(ctx, tx, "registry_read", pgtx.Args{"key": key, "subkey": subkey, "valuename": q.Get("name")})
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	rest.WriteJSON(w, 200, row)
}

// valueBody is one typed value: the type names of the Variant (0 integer,
// 1 numeric, 2 datetime, 3 string, 4 boolean) by name, the value in JSON.
type valueBody struct {
	ID     *string         `json:"id"`
	Key    *string         `json:"key"`
	SubKey *string         `json:"subkey"`
	Name   string          `json:"name"`
	Type   string          `json:"type"`
	Value  json.RawMessage `json:"value"`
}

// variantTypes: the Variant's type code and the SQL type pData is pinned to
// — pData is anynonarray, which no name alone can type.
var variantTypes = map[string]struct {
	code int
	sql  string
}{
	"integer":  {0, "integer"},
	"numeric":  {1, "numeric"},
	"datetime": {2, "timestamp"},
	"string":   {3, "text"},
	"boolean":  {4, "boolean"},
}

// typed turns the JSON value into what pgx sends for the pinned type: numbers and
// booleans as themselves, the rest as text.
func (b valueBody) typed() (any, error) {
	var v any
	if err := json.Unmarshal(b.Value, &v); err != nil || v == nil {
		return nil, problem.New(400, "validation", "Bad request", "value is required")
	}
	switch b.Type {
	case "integer":
		f, ok := v.(float64)
		if !ok || f != float64(int64(f)) {
			return nil, problem.New(400, "validation", "Bad request", "value must be an integer")
		}
		return int64(f), nil
	case "numeric":
		switch x := v.(type) {
		case float64:
			return strconv.FormatFloat(x, 'f', -1, 64), nil
		case string:
			return x, nil
		}
		return nil, problem.New(400, "validation", "Bad request", "value must be a number")
	case "boolean":
		bl, ok := v.(bool)
		if !ok {
			return nil, problem.New(400, "validation", "Bad request", "value must be a boolean")
		}
		return bl, nil
	default: // string, datetime
		s, ok := v.(string)
		if !ok {
			return nil, problem.New(400, "validation", "Bad request", "value must be a string")
		}
		return s, nil
	}
}

func (m *module) write(w http.ResponseWriter, r *http.Request) {
	var b valueBody
	raw, err := rest.ReadBody(r, &b)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	vt, ok := variantTypes[b.Type]
	switch {
	case b.Name == "":
		err = problem.New(400, "validation", "Bad request", "name is required")
	case b.ID == nil && (b.Key == nil || *b.Key == ""):
		err = problem.New(400, "validation", "Bad request", "id of the key, or key and subkey, is required")
	case b.ID != nil && !rest.IsUUID(*b.ID):
		err = problem.New(400, "validation", "Bad request", "id must be a UUID")
	case !ok:
		err = problem.New(400, "validation", "Bad request", "type must be one of integer, numeric, datetime, string, boolean")
	}
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	value, err := b.typed()
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	var id string
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, raw), func(ctx context.Context, tx pgx.Tx) error {
		return pgtx.CallScalar(ctx, tx, "registry_write", pgtx.Args{"id": b.ID, "key": b.Key, "subkey": b.SubKey, "valuename": b.Name, "type": vt.code, "data": pgtx.Typed{V: value, Type: vt.sql}}, &id)
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	body, _ := json.Marshal(map[string]any{"id": id, "name": b.Name, "type": b.Type})
	rest.WriteJSON(w, 200, body)
}

func (m *module) exec(w http.ResponseWriter, r *http.Request, fn string, args pgtx.Args) {
	if err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 204, nil), func(ctx context.Context, tx pgx.Tx) error {
		_, err := pgtx.Call(ctx, tx, fn, args)
		return err
	}); err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	w.WriteHeader(204)
}

func (m *module) deleteValueByID(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	m.exec(w, r, "registry_delete_value", pgtx.Args{"id": id, "key": nil, "subkey": nil, "valuename": nil})
}

func (m *module) deleteValue(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	key, subkey, err := keyRef(q)
	if err == nil && q.Get("name") == "" {
		err = problem.New(400, "validation", "Bad request", "name is required")
	}
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	m.exec(w, r, "registry_delete_value", pgtx.Args{"id": nil, "key": key, "subkey": subkey, "valuename": q.Get("name")})
}

func (m *module) deleteKey(w http.ResponseWriter, r *http.Request) {
	key, subkey, err := keyRef(r.URL.Query())
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	m.exec(w, r, "registry_delete_key", pgtx.Args{"key": key, "subkey": subkey})
}

func (m *module) deleteTree(w http.ResponseWriter, r *http.Request) {
	key, subkey, err := keyRef(r.URL.Query())
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	m.exec(w, r, "registry_delete_tree", pgtx.Args{"key": key, "subkey": subkey})
}
