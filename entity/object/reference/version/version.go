// Package version is the GoAPI package of the SQL entity version
// (db/sql/platform/entity/object/reference/version): /api/v2/versions over its api.*
// functions, in the pure declarative shape of lib/entity — the six routes of
// the form and nothing of the package's own:
//
//   - POST goes through api.add_version, not api.set_version(NULL, …): in this
//     family set_<x> resolves a NULL id by code (pId := coalesce(pId,
//     Get<X>(pCode))) and would update an existing row while answering 201
//     (v1's /version/set parity, not a create) — CreateFn of lib/entity.
//   - There is no api.delete_version and no delete flag in v1 /version/set: no
//     DELETE of the package's own; the workflow's actions stay reachable at
//     /api/v2/versions/{id}/actions/{action}.
//
// q= is matched over code and name (api.version's text columns, ILIKE
// applies). What the create cannot do without: code is the reference's
// business key (the family's rule: a codeless create is an accident, not a use
// case) — required on create.
package version

import (
	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// Prefix is what the package registers with the gateway.
const Prefix = "/api/v2/versions"

// Config wires the package.
type Config = entity.Config

// New returns the package as a platform.Module: the six routes of the form.
func New(cfg Config) platform.Module {
	return entity.New(entity.Entity{
		Name:         "version",
		Prefix:       Prefix,
		Resource:     rest.Resource{Prefix: Prefix, GetFn: "api.get_version", ListFn: "api.list_version", CountFn: "api.count_version"},
		SetFn:        "api.set_version",
		CreateFn:     "api.add_version", // api.set_version(NULL, …) is an upsert by code, not a create
		NewBody:      func() rest.Body { return &body{} },
		SearchFields: []string{"code", "name"},
	}, cfg)
}
