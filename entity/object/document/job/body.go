package job

import (
	"time"

	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
)

// body is the writable part of api.job: the parameters of api.set_job after
// the id, in their order — an unknown key is refused before the database, as
// CheckJsonbKeys does in rest.<x>. The json keys are the catalogue's
// parameter names without the p (pDateRun → daterun), as v1's /job/set
// payloads name them.
type body struct {
	Parent      *string `json:"parent"`
	Type        *string `json:"type"`
	Scheduler   *string `json:"scheduler"`
	Program     *string `json:"program"`
	DateRun     *string `json:"daterun"`
	Code        *string `json:"code"`
	Label       *string `json:"label"`
	Description *string `json:"description"`
}

// Str is a text argument: nil for absent (keep), the value otherwise.
func Str(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// IsTimestamp says whether s is a timestamp in one of the two shapes v1
// clients send: RFC 3339 or 'YYYY-MM-DD HH:MM:SS' — a bad literal at a
// timestamptz parameter is a 22007/22008 the pool reads as 500, not as the
// client's fault. (What the database makes of the instant is its own —
// db.ft_job_insert recomputes dateRun on insert whenever a scheduler is
// set, see the package's integration test.) The space layout is checked by
// round-trip: its fields would otherwise take unpadded digits. RFC 3339
// needs no round-trip — parsing already requires the zone, and a zero
// offset formats back as 'Z', not as '+00:00'.
func IsTimestamp(s string) bool {
	if _, err := time.Parse(time.RFC3339, s); err == nil {
		return true
	}
	t, err := time.Parse("2006-01-02 15:04:05", s)
	return err == nil && t.Format("2006-01-02 15:04:05") == s
}

// Validate: code, scheduler and program are what api.add_job cannot do
// without — db.job's scheduler and program are NOT NULL references no
// default fills, and code is the job's identity within its scope; on PATCH
// they may be absent (kept) but not emptied. A PATCH with nothing at all is
// an empty edit that still moves udate — refused, as the entity form
// refuses it. References are checked for the shape of a UUID and daterun
// for the shape of a timestamp here — a bad literal at a uuid or
// timestamptz parameter is a 22P02/22007 the pool reads as 500, not as the
// client's fault.
func (b *body) Validate(create bool) error {
	if err := rest.RequiredAll(create, rest.Req("code", b.Code), rest.Req("scheduler", b.Scheduler), rest.Req("program", b.Program)); err != nil {
		return err
	}
	empty := true
	for k, v := range b.Args(nil) {
		if k != "id" && v != nil {
			empty = false
			break
		}
	}
	if empty {
		return problem.New(400, "validation", "Bad request", "body must carry at least one field")
	}
	for _, ref := range []struct {
		name string
		v    *string
	}{{"parent", b.Parent}, {"type", b.Type}, {"scheduler", b.Scheduler}, {"program", b.Program}} {
		if ref.v != nil && !rest.IsUUID(*ref.v) {
			return problem.New(400, "validation", "Bad request", ref.name+" must be a UUID")
		}
	}
	if b.DateRun != nil && !IsTimestamp(*b.DateRun) {
		return problem.New(400, "validation", "Bad request", "daterun must be a timestamp (RFC 3339 or YYYY-MM-DD HH:MM:SS)")
	}
	return nil
}

// Args are the named arguments of api.set_job — the json keys, which are
// the catalogue's parameter names without the p. Absent and explicit null
// both mean "do not change".
func (b *body) Args(id any) pgtx.Args {
	return pgtx.Args{
		"id":          id,
		"parent":      Str(b.Parent),
		"type":        Str(b.Type),
		"scheduler":   Str(b.Scheduler),
		"program":     Str(b.Program),
		"daterun":     Str(b.DateRun),
		"code":        Str(b.Code),
		"label":       Str(b.Label),
		"description": Str(b.Description),
	}
}
