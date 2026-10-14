// Package vendor is the GoAPI package of the SQL entity vendor
// (db/sql/platform/entity/object/reference/vendor): /api/v2/vendors over its api.*
// functions, in the pure declarative shape of lib/entity — the six routes of
// the form and nothing of the package's own:
//
//   - POST goes through api.add_vendor, not api.set_vendor(NULL, …): in this
//     family set_<x> resolves a NULL id by code (pId := coalesce(pId,
//     Get<X>(pCode))) and would update an existing row while answering 201
//     (v1's /vendor/set parity, not a create) — CreateFn of lib/entity.
//   - There is no api.delete_vendor and no delete flag in v1 /vendor/set: no
//     DELETE of the package's own; the workflow's actions stay reachable at
//     /api/v2/vendors/{id}/actions/{action}.
//
// q= is matched over code and name (api.vendor's text columns, ILIKE applies).
// What the create cannot do without: code is the reference's business key (the
// family's rule: a codeless create is an accident, not a use case) — required
// on create.
package vendor

import (
	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// Prefix is what the package registers with the gateway.
const Prefix = "/api/v2/vendors"

// Config wires the package.
type Config = entity.Config

// New returns the package as a platform.Module: the six routes of the form.
func New(cfg Config) platform.Module {
	return entity.New(entity.Entity{
		Name:         "vendor",
		Prefix:       Prefix,
		Resource:     rest.Resource{Prefix: Prefix, GetFn: "api.get_vendor", ListFn: "api.list_vendor", CountFn: "api.count_vendor"},
		SetFn:        "api.set_vendor",
		CreateFn:     "api.add_vendor", // api.set_vendor(NULL, …) is an upsert by code, not a create
		NewBody:      func() rest.Body { return &body{} },
		SearchFields: []string{"code", "name"},
	}, cfg)
}
