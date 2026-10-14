// Package job is the GoAPI package of the SQL entity job
// (db/sql/platform/entity/object/document/job): /api/v2/jobs over its api.*
// functions, in the pure declarative shape of lib/entity — the six routes of
// the form and nothing of the package's own:
//
//   - POST goes through api.add_job, not api.set_job(NULL, …): in this
//     family set_<x> resolves a NULL id by code (pId := coalesce(pId,
//     GetJob(pCode))) and would update an existing row while answering 201
//     (v1's /job/set parity, not a create) — CreateFn of lib/entity.
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
	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// Prefix is what the package registers with the gateway.
const Prefix = "/api/v2/jobs"

// Doer runs the request transaction (pgtx.Runner in production).
type Doer = rest.Doer

// Config wires the package.
type Config = entity.Config

// New returns the package as a platform.Module: the six routes of the form.
func New(cfg Config) platform.Module {
	return entity.New(entity.Entity{
		Name:         "job",
		Prefix:       Prefix,
		Resource:     rest.Resource{Prefix: Prefix, GetFn: "api.get_job", ListFn: "api.list_job", CountFn: "api.count_job"},
		SetFn:        "api.set_job",
		CreateFn:     "api.add_job", // api.set_job(NULL, …) is an upsert by code, not a create
		NewBody:      func() rest.Body { return &body{} },
		SearchFields: []string{"code", "label"},
	}, cfg)
}
