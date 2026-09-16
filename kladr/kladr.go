// Package kladr is the GoAPI package of the SQL module kladr
// (db/sql/platform/kladr): the Russian address classifier as a tree, as
// /api/v2/kladr — v1 rest.kladr. Rows have integer ids; a node's history
// is its path to the root; /string renders a code as an address line.
// The tables are empty in every known deployment: the parity is of shape.
package kladr

import (
	"log/slog"
	"math"
	"net/http"
	"regexp"
	"strconv"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
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

// nodes lists api.address_tree; the id is an integer, read by RowHandler
// with the integer cast api.get_address_tree takes (IntID would cast bigint).
var nodes = rest.Resource{Prefix: "/api/v2/kladr", ListFn: "api.list_address_tree", CountFn: "api.count_address_tree", IntID: true}

// codeRe is a KLADR code: digits only.
var codeRe = regexp.MustCompile(`^[0-9]{1,20}$`)

func idOf(r *http.Request) ([]any, error) {
	id, err := rest.IntIDOf(r)
	if err != nil {
		return nil, err
	}
	if id > math.MaxInt32 {
		return nil, problem.New(400, "validation", "Bad request", "id is out of range")
	}
	return []any{int32(id)}, nil
}

// intParam reads a non-negative integer query parameter, absent is 0.
func intParam(r *http.Request, name string) (int32, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 31)
	if err != nil || n < 0 {
		return 0, problem.New(400, "validation", "Bad request", name+" must be a non-negative integer")
	}
	return int32(n), nil
}

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger}
}

func (m *module) Name() string       { return "kladr" }
func (m *module) Prefixes() []string { return []string{nodes.Prefix} }

// Routes registers the tree on the shared mux.
func (m *module) Routes(mux *http.ServeMux) {
	d, log := m.cfg.Doer, m.log
	p := nodes.Prefix
	mux.HandleFunc("GET "+p, nodes.List(d, log))
	mux.HandleFunc("GET "+p+"/{id}", rest.RowHandler(d, log, "SELECT row_to_json(t) FROM api.get_address_tree($1::integer) t", idOf))
	mux.HandleFunc("GET "+p+"/{id}/history", rest.RowsOf(d, log, "SELECT row_to_json(t) FROM api.get_address_tree_history($1::integer) t", idOf))
	// string is a query, not an id: the code is a KLADR code, not a row
	mux.HandleFunc("GET "+p+"/string", rest.RowHandler(d, log, "SELECT json_build_object('address', api.get_address_tree_string($1, $2::integer, $3::integer))", stringArgs))
}

// stringArgs is ?code=&short=&level= of /string.
func stringArgs(r *http.Request) ([]any, error) {
	code := r.URL.Query().Get("code")
	if !codeRe.MatchString(code) {
		return nil, problem.New(400, "validation", "Bad request", "code must be a KLADR code")
	}
	short, err := intParam(r, "short")
	if err != nil {
		return nil, err
	}
	level, err := intParam(r, "level")
	if err != nil {
		return nil, err
	}
	return []any{code, short, level}, nil
}
