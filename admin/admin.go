// Package admin is the GoAPI package of the SQL module admin
// (db/sql/platform/admin): users, groups, areas, interfaces and their
// memberships, sessions — over the module's api.* functions, in the /api/v2
// /api/v2 shape. The v1 routes /user/* and /admin/user/*
// call the same functions; here they are one resource, and who may see or
// change what is the database's decision, as it always was.
package admin

import (
	"log/slog"
	"net/http"
	"time"

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
	cfg  Config
	log  *slog.Logger
	idem *rest.Idempotency
}

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger, idem: rest.NewIdempotency(24 * time.Hour)}
}

func (m *module) Name() string { return "admin" }
func (m *module) Prefixes() []string {
	return []string{users.Prefix, groups.Prefix, areas.Prefix, areaTypes.Prefix, interfaces.Prefix, sessions.Prefix}
}

// Routes registers the module's resources on the shared mux.
func (m *module) Routes(mux *http.ServeMux) {
	m.userRoutes(mux)
	m.collectionRoutes(mux)
}
