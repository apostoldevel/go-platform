// Package agent is the GoAPI package of the SQL entity agent
// (db/sql/platform/entity/object/reference/agent): /api/v2/agents over its api.*
// functions, in the pure declarative shape of lib/entity — the six routes of
// the form and nothing of the package's own:
//
//   - POST goes through api.add_agent, not api.set_agent(NULL, …): in this
//     family set_<x> resolves a NULL id by code (pId := coalesce(pId,
//     Get<X>(pCode))) and would update an existing row while answering 201
//     (v1's /agent/set parity, not a create) — CreateFn of lib/entity.
//   - There is no api.delete_agent and no delete flag in v1 /agent/set: no
//     DELETE of the package's own; the workflow's actions stay reachable at
//     /api/v2/agents/{id}/actions/{action}.
//
// q= is matched over code and name (api.agent's text columns, ILIKE applies).
// What the create cannot do without: an agent is a delivery channel of
// messages (SMTP, FCM, …); its vendor is a NOT NULL reference no default
// fills, and code is its business key — both are required on create.
package agent

import (
	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// Prefix is what the package registers with the gateway.
const Prefix = "/api/v2/agents"

// Config wires the package.
type Config = entity.Config

// New returns the package as a platform.Module: the six routes of the form.
func New(cfg Config) platform.Module {
	return entity.New(entity.Entity{
		Name:         "agent",
		Prefix:       Prefix,
		Resource:     rest.Resource{Prefix: Prefix, GetFn: "api.get_agent", ListFn: "api.list_agent", CountFn: "api.count_agent"},
		SetFn:        "api.set_agent",
		CreateFn:     "api.add_agent", // api.set_agent(NULL, …) is an upsert by code, not a create
		NewBody:      func() rest.Body { return &body{} },
		SearchFields: []string{"code", "name"},
	}, cfg)
}
