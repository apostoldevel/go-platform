// Package reference is the GoAPI package of the SQL entity reference
// (db/sql/platform/entity/object/reference): /api/v2/references over its api.*
// functions, in the pure declarative shape of lib/entity — the six routes of
// the form and nothing of the package's own:
//
//   - POST goes through api.add_reference, not api.set_reference(NULL, …): in this
//     family set_<x> resolves a NULL id by code (pId := coalesce(pId,
//     Get<X>(pCode))) and would update an existing row while answering 201
//     (v1's /reference/set parity, not a create) — CreateFn of lib/entity.
//   - There is no api.delete_reference and no delete flag in v1 /reference/set: no
//     DELETE of the package's own; the workflow's actions stay reachable at
//     /api/v2/references/{id}/actions/{action}.
//
// q= is matched over code and name (api.reference's text columns, ILIKE
// applies). What the create cannot do without: the generic reference: the base
// of the family (vendor, agent, program, …) — its rows are of the type given;
// code is the reference's business key and is required on create.
package reference

import (
	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// Prefix is what the package registers with the gateway.
const Prefix = "/api/v2/references"

// Config wires the package.
type Config = entity.Config

// New returns the package as a platform.Module: the six routes of the form.
func New(cfg Config) platform.Module {
	return entity.New(entity.Entity{
		Name:         "reference",
		Prefix:       Prefix,
		Resource:     rest.Resource{Prefix: Prefix, GetFn: "api.get_reference", ListFn: "api.list_reference", CountFn: "api.count_reference"},
		SetFn:        "api.set_reference",
		CreateFn:     "api.add_reference", // api.set_reference(NULL, …) is an upsert by code, not a create
		NewBody:      func() rest.Body { return &body{} },
		SearchFields: []string{"code", "name"},
	}, cfg)
}
