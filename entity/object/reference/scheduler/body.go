package scheduler

import (
	"time"

	"github.com/apostoldevel/go-platform/lib/entity"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// body is the writable part of api.scheduler: the parameters of
// api.set_scheduler after the id, in their order — an unknown key is refused
// before the database, as CheckJsonbKeys does in rest.<x>. The json keys are
// the catalogue's parameter names without the p.
type body struct {
	Parent      *string `json:"parent"`
	Type        *string `json:"type"`
	Code        *string `json:"code"`
	Name        *string `json:"name"`
	Period      *string `json:"period"`
	DateStart   *string `json:"datestart"`
	DateStop    *string `json:"datestop"`
	Description *string `json:"description"`
}

// str is a text argument: nil for absent (keep), the value otherwise.
func str(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// isTimestamp says whether s is a timestamp in one of the two shapes v1
// clients send: RFC 3339 or 'YYYY-MM-DD HH:MM:SS'. The space layout is
// checked by round-trip: its fields would otherwise take unpadded digits.
func isTimestamp(s string) bool {
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return true
	}
	t, err := time.Parse("2006-01-02 15:04:05", s)
	return err == nil && t.Format("2006-01-02 15:04:05") == s
}

// Validate: what api.add_scheduler cannot do without is required on create
// (code); on PATCH it may be absent (kept) but not emptied. References are
// checked for the shape of a UUID here — a bad literal at a uuid parameter is
// a 22P02 the pool reads as the client's data, not as a fault of its own.
func (b *body) Validate(create bool) error {
	if err := rest.RequiredAll(create, rest.Req("code", b.Code)); err != nil {
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
	if b.DateStart != nil && !isTimestamp(*b.DateStart) {
		return problem.New(400, "validation", "Bad request", "datestart must be a timestamp (RFC 3339 or YYYY-MM-DD HH:MM:SS)")
	}
	if b.DateStop != nil && !isTimestamp(*b.DateStop) {
		return problem.New(400, "validation", "Bad request", "datestop must be a timestamp (RFC 3339 or YYYY-MM-DD HH:MM:SS)")
	}
	return nil
}

// Args are the named arguments of api.set_scheduler — entity.Named over
// positional, the json keys in the order of the catalogue's parameters.
func (b *body) Args(id any) pgtx.Args { return entity.Named(b, b.positional(id)) }

// positional are the 9 arguments of api.set_scheduler (the id, then the 8
// of the body) — api.add_scheduler takes the 8. Absent and explicit null both
// mean "do not change".
func (b *body) positional(id any) []any {
	return []any{id,
		str(b.Parent),
		str(b.Type),
		str(b.Code),
		str(b.Name),
		str(b.Period),
		str(b.DateStart),
		str(b.DateStop),
		str(b.Description)}
}
