// Package error is the GoAPI package of the SQL module error
// (db/sql/platform/error): the catalogue of ERR-GGG-CCC codes behind every
// problem+json answer, as /api/v2/errors — v1 rest.error. The catalogue has
// no api.delete_error, so the resource has four verbs; a code is also read
// by value at /api/v2/errors/by-code/{code}.
//
// The package is named after its SQL module, which is also the name of Go's
// predeclared error type: import it under an alias (platformerror), or the
// type is shadowed in the importing file.
package error

import (
	"log/slog"
	"net/http"
	"regexp"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
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
	cfg  Config
	log  *slog.Logger
	idem *rest.Idempotency
}

var errors = rest.Writable{
	Resource: rest.Resource{Prefix: "/api/v2/errors", GetFn: "api.get_error", ListFn: "api.list_error", CountFn: "api.count_error"},
	SetFn:    "api.set_error",
	NewBody:  func() rest.Body { return &body{} }, // absent severity/category default in api.add_error (db-platform ≥ 1.2.21)
}

// codeRe is the shape of a catalogue code: ERR-<http group>-<number>.
var codeRe = regexp.MustCompile(`^ERR-[0-9]{3}-[0-9]{3}$`)

// body is the parameters of api.set_error after the id.
type body struct {
	Code        *string `json:"code"`
	HTTPCode    *int    `json:"http_code"`
	Severity    *string `json:"severity"`
	Category    *string `json:"category"`
	Message     *string `json:"message"`
	Description *string `json:"description"`
	Resolution  *string `json:"resolution"`
}

func (b *body) Validate(create bool) error {
	if err := rest.Required("code", b.Code, create); err != nil {
		return err
	}
	if b.Code != nil && !codeRe.MatchString(*b.Code) {
		return problem.New(400, "validation", "Bad request", "code must be ERR-GGG-CCC")
	}
	if create && b.HTTPCode == nil {
		return problem.New(400, "validation", "Bad request", "http_code is required")
	}
	return nil
}

func (b *body) Args(id any) pgtx.Args {
	return pgtx.Args{"id": id, "code": b.Code, "httpcode": b.HTTPCode, "severity": b.Severity, "category": b.Category, "message": b.Message, "description": b.Description, "resolution": b.Resolution}
}

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger, idem: rest.NewIdempotency(24 * time.Hour)}
}

func (m *module) Name() string       { return "error" }
func (m *module) Prefixes() []string { return []string{errors.Prefix} }

// Routes registers the catalogue and the lookup by code on the shared mux.
func (m *module) Routes(mux *http.ServeMux) {
	errors.Routes(mux, m.cfg.Doer, m.idem, m.log)
	mux.HandleFunc("GET "+errors.Prefix+"/by-code/{code}", rest.CallRowHandler(m.cfg.Doer, m.log,
		"get_error_by_code", func(r *http.Request) (pgtx.Args, error) {
			code := r.PathValue("code")
			if !codeRe.MatchString(code) {
				return nil, problem.New(400, "validation", "Bad request", "code must be ERR-GGG-CCC")
			}
			return pgtx.Args{"code": code}, nil
		}))
}
