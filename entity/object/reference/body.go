package reference

import (
	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// body is the writable part of api.reference: the parameters of
// api.set_reference after the id, in their order — an unknown key is refused
// before the database, as CheckJsonbKeys does in rest.<x>. The json keys are
// the catalogue's parameter names without the p.
type body struct {
	Parent      *string `json:"parent"`
	Type        *string `json:"type"`
	Code        *string `json:"code"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// str is a text argument: nil for absent (keep), the value otherwise.
func str(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// Validate: what api.add_reference cannot do without is required on create
// (type, code) — api.add_reference passes type through as given (no default)
// and code is the reference's business key; on PATCH it may be absent (kept)
// but not emptied. References are checked for the shape of a UUID here — a bad
// literal at a uuid parameter is a 22P02 the pool reads as the client's data,
// not as a fault of its own.
func (b *body) Validate(create bool) error {
	if err := rest.RequiredAll(create, rest.Req("type", b.Type), rest.Req("code", b.Code)); err != nil {
		return err
	}
	for _, ref := range []struct {
		name string
		v    *string
	}{{"parent", b.Parent}, {"type", b.Type}} {
		if ref.v != nil && !rest.IsUUID(*ref.v) {
			return problem.New(400, "validation", "Bad request", ref.name+" must be a UUID")
		}
	}
	return nil
}

// Args are the named arguments of api.set_reference — entity.Named over
// positional, the json keys in the order of the catalogue's parameters.
func (b *body) Args(id any) pgtx.Args { return entity.Named(b, b.positional(id)) }

// positional are the 6 arguments of api.set_reference (the id, then the 5
// of the body) — api.add_reference takes the 5. Absent and explicit null both
// mean "do not change".
func (b *body) positional(id any) []any {
	return []any{id,
		str(b.Parent),
		str(b.Type),
		str(b.Code),
		str(b.Name),
		str(b.Description)}
}
