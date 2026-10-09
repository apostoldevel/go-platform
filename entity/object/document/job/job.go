// Package job is the GoAPI package of the SQL entity job
// (db/sql/platform/entity/object/document/job): /api/v2/jobs over its api.*
// functions — the six routes of the entity form. The declarative form of
// configuration/csms/lib/entity cannot be shared across the module boundary
// (the platform never imports the project that builds on it), so this
// package repeats the form hand-written over lib/rest, as observer and
// current do for their shapes:
//
//   - POST goes through api.add_job, not api.set_job(NULL, …): in this
//     family set_<x> resolves a NULL id by code (pId := coalesce(pId,
//     GetJob(pCode))) and would update an existing row while answering 201
//     (v1's /job/set parity, not a create) — rest.Writable.CreateFn.
//   - There is no api.delete_job and no delete flag in v1 /job/set: no
//     DELETE of the package's own; the workflow's actions stay reachable at
//     /jobs/{id}/actions/{action}.
//
// q= is matched over code and label (kernel.ObjectJob flattens both to text
// columns, ILIKE applies). What api.add_job cannot do without: db.job's
// scheduler and program are NOT NULL references no default fills, and code
// is the job's identity within its scope (the unique index, and what
// set_job resolves a NULL id by) — all three are required on create.
package job

import (
	"log/slog"
	"net/http"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// Prefix is what the package registers with the gateway.
const Prefix = "/api/v2/jobs"

// Doer runs the request transaction (pgtx.Runner in production).
type Doer = rest.Doer

// Config wires the package.
type Config struct {
	Doer   Doer
	Logger *slog.Logger
}

type module struct {
	cfg      Config
	log      *slog.Logger
	idem     *rest.Idempotency
	resource rest.Resource
	writable rest.Writable
}

// New returns the package as a platform.Module: the six routes of the form.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	m := &module{cfg: cfg, log: cfg.Logger, idem: rest.NewIdempotency(24 * time.Hour)}
	m.resource = rest.Resource{Prefix: Prefix, GetFn: "api.get_job", ListFn: "api.list_job", CountFn: "api.count_job"}
	// CreateFn, not SetFn(NULL, …): api.set_job(NULL, …) is an upsert by
	// code, not a create (see the package doc)
	m.writable = rest.Writable{Resource: m.resource, SetFn: "api.set_job", CreateFn: "api.add_job", NewBody: func() rest.Body { return &body{} }}
	return m
}

func (m *module) Name() string       { return "job" }
func (m *module) Prefixes() []string { return []string{Prefix} }

// Routes registers the six routes of the form on the shared mux. There is
// no route of the package's own: no api.delete_job, no v1 delete flag.
func (m *module) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+Prefix, m.list)
	mux.HandleFunc("GET "+Prefix+"/{id}", rest.CallRowHandler(m.cfg.Doer, m.log, m.resource.GetFn, idArg))
	mux.HandleFunc("POST "+Prefix, m.writable.Create(m.cfg.Doer, m.idem, m.log))
	mux.HandleFunc("PATCH "+Prefix+"/{id}", m.writable.Update(m.cfg.Doer, m.log))
	mux.HandleFunc("POST "+Prefix+"/{id}/actions/{action}", m.action)
	mux.HandleFunc("GET "+Prefix+"/{id}/methods", m.methods)
}
