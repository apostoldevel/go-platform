package message

import (
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// body is the writable part of api.message: the parameters of
// api.set_message after the id, in their order — an unknown key is refused
// before the database, as CheckJsonbKeys does in rest.<x>. The json keys are
// the catalogue's parameter names without the p.
type body struct {
	Parent      *string `json:"parent" format:"uuid" doc:"Parent object (UUID), or absent."`
	Type        *string `json:"type" format:"uuid" required:"create" doc:"Type of the message (UUID of a type of the message entity; for example message.outbox). Required on create; no default is applied. On PATCH absent keeps the type."`
	Agent       *string `json:"agent" format:"uuid" required:"create" doc:"Agent that delivers the message (UUID of an agent). Required on create."`
	Code        *string `json:"code" required:"create" doc:"Code of the message: its business key. Required on create; on PATCH it may be absent (kept) but not emptied."`
	Profile     *string `json:"profile" required:"create" doc:"Profile of the message on the agent (db.message.profile, NOT NULL). Required on create."`
	Address     *string `json:"address" required:"create" doc:"Address of the message on the agent (db.message.address, NOT NULL). Required on create."`
	Subject     *string `json:"subject" doc:"Subject of the message."`
	Content     *string `json:"content" doc:"Content of the message."`
	Label       *string `json:"label" doc:"Label of the message in the session's locale."`
	Description *string `json:"description" doc:"Description of the message in the session's locale."`
}

// str is a text argument: nil for absent (keep), the value otherwise.
func str(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// Validate: what api.add_message cannot do without is required on create
// (type, agent, code, profile, address) — api.add_message passes type through
// as given and db.message's agent, code, profile and address are NOT NULL;
// subject and content are nullable; on PATCH it may be absent (kept) but not
// emptied. References are checked for the shape of a UUID here — a bad literal
// at a uuid parameter is a 22P02 the pool reads as the client's data, not as a
// fault of its own.
func (b *body) Validate(create bool) error {
	if err := rest.RequiredAll(create, rest.Req("type", b.Type), rest.Req("agent", b.Agent), rest.Req("code", b.Code), rest.Req("profile", b.Profile), rest.Req("address", b.Address)); err != nil {
		return err
	}
	for _, ref := range []struct {
		name string
		v    *string
	}{{"parent", b.Parent}, {"type", b.Type}, {"agent", b.Agent}} {
		if ref.v != nil && !rest.IsUUID(*ref.v) {
			return problem.New(400, "validation", "Bad request", ref.name+" must be a UUID")
		}
	}
	return nil
}

// Args are the named arguments of api.set_message — entity.Named over
// positional, the json keys in the order of the catalogue's parameters.
func (b *body) Args(id any) pgtx.Args { return entity.Named(b, b.positional(id)) }

// positional are the 11 arguments of api.set_message (the id, then the 10
// of the body) — api.add_message takes the 10. Absent and explicit null both
// mean "do not change".
func (b *body) positional(id any) []any {
	return []any{id,
		str(b.Parent),
		str(b.Type),
		str(b.Agent),
		str(b.Code),
		str(b.Profile),
		str(b.Address),
		str(b.Subject),
		str(b.Content),
		str(b.Label),
		str(b.Description)}
}
