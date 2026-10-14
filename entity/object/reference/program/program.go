// Package program is the GoAPI package of the SQL entity program
// (db/sql/platform/entity/object/reference/program): /api/v2/programs over its api.*
// functions, in the pure declarative shape of lib/entity — the six routes of
// the form and nothing of the package's own:
//
//   - POST goes through api.add_program, not api.set_program(NULL, …): in this
//     family set_<x> resolves a NULL id by code (pId := coalesce(pId,
//     Get<X>(pCode))) and would update an existing row while answering 201
//     (v1's /program/set parity, not a create) — CreateFn of lib/entity.
//   - There is no api.delete_program and no delete flag in v1 /program/set: no
//     DELETE of the package's own; the workflow's actions stay reachable at
//     /api/v2/programs/{id}/actions/{action}.
//
// q= is matched over code and name (api.program's text columns, ILIKE
// applies). What the create cannot do without: a program is a stored source
// (db.program.body is NOT NULL, no default), and code is its business key —
// both are required on create.
package program

import (
	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// Prefix is what the package registers with the gateway.
const Prefix = "/api/v2/programs"

// Config wires the package.
type Config = entity.Config

// New returns the package as a platform.Module: the six routes of the form.
func New(cfg Config) platform.Module {
	return entity.New(entity.Entity{
		Name:         "program",
		Prefix:       Prefix,
		Resource:     rest.Resource{Prefix: Prefix, GetFn: "api.get_program", ListFn: "api.list_program", CountFn: "api.count_program"},
		SetFn:        "api.set_program",
		CreateFn:     "api.add_program", // api.set_program(NULL, …) is an upsert by code, not a create
		NewBody:      func() rest.Body { return &body{} },
		SearchFields: []string{"code", "name"},
	}, cfg)
}
