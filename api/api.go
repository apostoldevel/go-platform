// Package api is the GoAPI package of the SQL module api
// (db/sql/platform/api): of its v1 routes only the API journal is a
// resource — /api/v2/api-log over api.log (v1 /admin/api/log/*); the
// rest of rest.api (ping, time, authenticate, authorize, su, run) is not
// carried to v2 by design (README, "What v1 has that v2 does not"), and
// /search, /locale,
// /entity … belong to the packages that own their api.* functions.
package api

import (
	"log/slog"
	"net/http"

	platform "github.com/apostoldevel/go-platform"
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

var apiLog = rest.Resource{Prefix: "/api/v2/api-log", GetFn: "api.get_log", ListFn: "api.list_log", CountFn: "api.count_log", IntID: true}

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger}
}

func (m *module) Name() string       { return "api" }
func (m *module) Prefixes() []string { return []string{apiLog.Prefix} }

// Routes registers the journal: list and one row, nothing else — the
// journal is written by api.log_request on every request.
func (m *module) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+apiLog.Prefix, apiLog.List(m.cfg.Doer, m.log))
	mux.HandleFunc("GET "+apiLog.Prefix+"/{id}", apiLog.Get(m.cfg.Doer, m.log))
}
