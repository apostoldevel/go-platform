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
	Parent      *string `json:"parent"`
	Type        *string `json:"type"`
	Agent       *string `json:"agent"`
	Code        *string `json:"code"`
	Profile     *string `json:"profile"`
	Address     *string `json:"address"`
	Subject     *string `json:"subject"`
	Content     *string `json:"content"`
	Label       *string `json:"label"`
	Description *string `json:"description"`
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
