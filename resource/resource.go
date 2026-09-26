// Package resource is the GoAPI package of the SQL module resource
// (db/sql/platform/resource): the tree of localized resources (texts,
// templates, files by node) as /api/v2/resources — v1 rest.resource. A row
// is read in the session's locale; a write without locale_code goes to it.
package resource

import (
	"log/slog"
	"net/http"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
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

var resources = rest.Writable{
	Resource: rest.Resource{Prefix: "/api/v2/resources", GetFn: "api.get_resource", ListFn: "api.list_resource", CountFn: "api.count_resource"},
	SetFn:    "api.set_resource",
	NewBody:  func() rest.Body { return &body{} },
	DeleteFn: "api.delete_resource",
}

// body is the parameters of api.set_resource after the id.
type body struct {
	Root        *string `json:"root"`
	Node        *string `json:"node"`
	Type        *string `json:"type"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Encoding    *string `json:"encoding"`
	Data        *string `json:"data"`
	Sequence    *int    `json:"sequence"`
	LocaleCode  *string `json:"locale_code"`
}

func (b *body) Validate(create bool) error { return rest.Required("name", b.Name, create) }
func (b *body) Args(id any) pgtx.Args {
	a := pgtx.Args{"id": id, "root": b.Root, "node": b.Node, "type": b.Type, "name": b.Name, "description": b.Description, "encoding": b.Encoding, "data": b.Data, "sequence": b.Sequence}
	// pLocaleCode defaults to the session's locale (locale_code()); an explicit NULL would not — absent, the key is left out
	if b.LocaleCode != nil {
		a["localecode"] = b.LocaleCode
	}
	return a
}

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger, idem: rest.NewIdempotency(24 * time.Hour)}
}

func (m *module) Name() string       { return "resource" }
func (m *module) Prefixes() []string { return []string{resources.Prefix} }

// Routes registers the resource on the shared mux.
func (m *module) Routes(mux *http.ServeMux) { resources.Routes(mux, m.cfg.Doer, m.idem, m.log) }
