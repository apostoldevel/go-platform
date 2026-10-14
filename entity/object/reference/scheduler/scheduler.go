// Package scheduler is the GoAPI package of the SQL entity scheduler
// (db/sql/platform/entity/object/reference/scheduler): /api/v2/schedulers over its api.*
// functions, in the pure declarative shape of lib/entity — the six routes of
// the form and nothing of the package's own:
//
//   - POST goes through api.add_scheduler, not api.set_scheduler(NULL, …): in this
//     family set_<x> resolves a NULL id by code (pId := coalesce(pId,
//     Get<X>(pCode))) and would update an existing row while answering 201
//     (v1's /scheduler/set parity, not a create) — CreateFn of lib/entity.
//   - There is no api.delete_scheduler and no delete flag in v1 /scheduler/set: no
//     DELETE of the package's own; the workflow's actions stay reachable at
//     /api/v2/schedulers/{id}/actions/{action}.
//
// q= is matched over code and name (api.scheduler's text columns, ILIKE
// applies). What the create cannot do without: period is an interval literal,
// datestart and datestop are timestamps (RFC 3339 or YYYY-MM-DD HH:MM:SS); the
// database fills datestart and datestop when absent. Code is the scheduler's
// business key and is required on create.
package scheduler

import (
	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// Prefix is what the package registers with the gateway.
const Prefix = "/api/v2/schedulers"

// Config wires the package.
type Config = entity.Config

// New returns the package as a platform.Module: the six routes of the form.
func New(cfg Config) platform.Module {
	return entity.New(entity.Entity{
		Name:         "scheduler",
		Prefix:       Prefix,
		Resource:     rest.Resource{Prefix: Prefix, GetFn: "api.get_scheduler", ListFn: "api.list_scheduler", CountFn: "api.count_scheduler"},
		SetFn:        "api.set_scheduler",
		CreateFn:     "api.add_scheduler", // api.set_scheduler(NULL, …) is an upsert by code, not a create
		NewBody:      func() rest.Body { return &body{} },
		SearchFields: []string{"code", "name"},
	}, cfg)
}
