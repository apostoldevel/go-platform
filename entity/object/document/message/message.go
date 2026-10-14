// Package message is the GoAPI package of the SQL entity message
// (db/sql/platform/entity/object/document/message): /api/v2/messages over its api.*
// functions, in the pure declarative shape of lib/entity — the six routes of
// the form and nothing of the package's own:
//
//   - POST goes through api.add_message, not api.set_message(NULL, …): in this
//     family set_<x> resolves a NULL id by code (pId := coalesce(pId,
//     Get<X>(pCode))) and would update an existing row while answering 201
//     (v1's /message/set parity, not a create) — CreateFn of lib/entity.
//   - There is no api.delete_message and no delete flag in v1 /message/set: no
//     DELETE of the package's own; the workflow's actions stay reachable at
//     /api/v2/messages/{id}/actions/{action}.
//
// q= is matched over code and subject (api.message's text columns, ILIKE
// applies). What the create cannot do without: a message is a row of the
// outbox/inbox: db.message's agent, code, profile and address are NOT NULL and
// no default fills them, and type is passed through as given — all five are
// required on create; subject and content are nullable. The send_* functions
// of the entity (send_message, send_mail, …) are not a template and not routes
// of this package.
package message

import (
	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// Prefix is what the package registers with the gateway.
const Prefix = "/api/v2/messages"

// Config wires the package.
type Config = entity.Config

// New returns the package as a platform.Module: the six routes of the form.
func New(cfg Config) platform.Module {
	return entity.New(entity.Entity{
		Name:         "message",
		Prefix:       Prefix,
		Resource:     rest.Resource{Prefix: Prefix, GetFn: "api.get_message", ListFn: "api.list_message", CountFn: "api.count_message"},
		SetFn:        "api.set_message",
		CreateFn:     "api.add_message", // api.set_message(NULL, …) is an upsert by code, not a create
		NewBody:      func() rest.Body { return &body{} },
		SearchFields: []string{"code", "subject"},
	}, cfg)
}
