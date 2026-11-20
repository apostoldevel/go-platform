package agent

import (
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// body is the writable part of api.agent: the parameters of
// api.set_agent after the id, in their order — an unknown key is refused
// before the database, as CheckJsonbKeys does in rest.<x>. The json keys are
// the catalogue's parameter names without the p.
type body struct {
	Parent      *string `json:"parent" format:"uuid" doc:"Parent object (UUID), or absent."`
	Type        *string `json:"type" format:"uuid" doc:"Type of the agent (UUID of a type of the agent entity). On create the type system.agent is used when absent; on PATCH absent keeps the type."`
	Vendor      *string `json:"vendor" format:"uuid" required:"create" doc:"Vendor of the agent (UUID of a vendor). Required on create; on PATCH absent keeps it."`
	Code        *string `json:"code" required:"create" doc:"Code of the agent: its business key. Required on create; on PATCH it may be absent (kept) but not emptied."`
	Name        *string `json:"name" doc:"Name of the agent in the session's locale."`
	Description *string `json:"description" doc:"Description of the agent in the session's locale."`
}

// str is a text argument: nil for absent (keep), the value otherwise.
func str(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// Validate: what api.add_agent cannot do without is required on create (code,
// vendor); on PATCH it may be absent (kept) but not emptied. References are
// checked for the shape of a UUID here — a bad literal at a uuid parameter is
// a 22P02 the pool reads as the client's data, not as a fault of its own.
func (b *body) Validate(create bool) error {
	if err := rest.RequiredAll(create, rest.Req("vendor", b.Vendor), rest.Req("code", b.Code)); err != nil {
		return err
	}
	for _, ref := range []struct {
		name string
		v    *string
	}{{"parent", b.Parent}, {"type", b.Type}, {"vendor", b.Vendor}} {
		if ref.v != nil && !rest.IsUUID(*ref.v) {
			return problem.New(400, "validation", "Bad request", ref.name+" must be a UUID")
		}
	}
	return nil
}

// Args are the named arguments of api.set_agent — entity.Named over
// positional, the json keys in the order of the catalogue's parameters.
func (b *body) Args(id any) pgtx.Args { return entity.Named(b, b.positional(id)) }

// positional are the 7 arguments of api.set_agent (the id, then the 6
// of the body) — api.add_agent takes the 6. Absent and explicit null both
// mean "do not change".
func (b *body) positional(id any) []any {
	return []any{id,
		str(b.Parent),
		str(b.Type),
		str(b.Vendor),
		str(b.Code),
		str(b.Name),
		str(b.Description)}
}
