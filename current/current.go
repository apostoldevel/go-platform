// Package current is the GoAPI package of the SQL modules current and session
// (db/sql/platform/current, db/sql/platform/session): the caller's own session
// as one resource, /api/v2/me — v1 rest.current (seven reads) and rest.session
// (four writes). GET answers {user, area, interface, locale, oper_date} in one
// object; PATCH sets any of area, interface, locale, oper_date and answers the
// same object. The session code itself is never on the wire: v2 knows it from
// the JWT.
package current

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
	"github.com/jackc/pgx/v5"
)

// Doer runs the request transaction (pgtx.Runner in production).
type Doer = rest.Doer

// Config wires the package.
type Config struct {
	Doer   Doer
	Logger *slog.Logger
}

type module struct {
	cfg Config
	log *slog.Logger
}

const prefix = "/api/v2/me"

// readMe is the v1 reads as one object: user, area, interface, locale — the
// first row of api.current_<x>() or null — and the scalar oper_date.
func readMe(ctx context.Context, tx pgx.Tx) (json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for _, k := range []string{"user", "area", "interface", "locale"} {
		row, err := pgtx.CallRow(ctx, tx, "current_"+k, nil)
		if errors.Is(err, pgtx.ErrNoRow) || (err == nil && row == nil) {
			row = json.RawMessage("null")
		} else if err != nil {
			return nil, err
		}
		out[k] = row
	}
	od, err := pgtx.CallRow(ctx, tx, "oper_date", nil)
	if err != nil {
		return nil, err
	}
	if od == nil {
		od = json.RawMessage("null")
	}
	out["oper_date"] = od
	return json.Marshal(out)
}

// body is what PATCH /me may set. area, interface and locale take a uuid or
// a code (the api.* overloads); oper_date is RFC 3339, null clears it.
type body struct {
	Area      *string         `json:"area"`
	Interface *string         `json:"interface"`
	Locale    *string         `json:"locale"`
	OperDate  json.RawMessage `json:"oper_date"` // "null" when given as null, nil when absent
}

// sessionCall is the api.set_session_<name> call for a uuid or a code:
// locale names them apart (pLocale uuid | pCode text); area has one name for
// both overloads, so a code goes to api.set_session_area_by_code — the one
// daemon.call can tell apart by name, and the same on the direct road;
// interface takes a uuid only (a code is refused before the database, see
// patch).
func sessionCall(name, v string) (string, pgtx.Args) {
	isID := rest.IsUUID(v)
	switch {
	case name == "locale" && isID:
		return "set_session_locale", pgtx.Args{"locale": v}
	case name == "locale":
		return "set_session_locale", pgtx.Args{"code": v}
	case name == "area" && !isID:
		return "set_session_area_by_code", pgtx.Args{"code": v}
	}
	return "set_session_" + name, pgtx.Args{name: v}
}

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger}
}

func (m *module) Name() string       { return "current" }
func (m *module) Prefixes() []string { return []string{prefix} }

// Routes registers the resource on the shared mux.
func (m *module) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+prefix, m.get)
	mux.HandleFunc("PATCH "+prefix, m.patch)
}

func (m *module) get(w http.ResponseWriter, r *http.Request) {
	var row json.RawMessage
	if err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		row, err = readMe(ctx, tx)
		return err
	}); err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	rest.WriteJSON(w, 200, row)
}

// patch is PATCH /me: the body is decided first (nothing to set is 400),
// then every present field is set in the one transaction and the whole
// object is read back — the answer is what GET would say next.
func (m *module) patch(w http.ResponseWriter, r *http.Request) {
	var b body
	raw, err := rest.ReadBody(r, &b)
	if err == nil && len(raw) == 0 {
		err = problem.New(400, "validation", "Bad request", "a JSON body is required")
	}
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	// encoding/json drops an explicit null into a nil pointer — presence is read from the keys
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(raw, &keys)
	_, hasOperDate := keys["oper_date"]
	var operDate *time.Time
	// the three api.set_session_<x> of the body, in the order they run
	fields := []struct {
		name string
		v    *string
	}{{"area", b.Area}, {"interface", b.Interface}, {"locale", b.Locale}}
	for _, f := range fields {
		if f.v != nil && *f.v == "" {
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", f.name+" must not be empty"))
			return
		}
		// api.set_session_interface takes a uuid only: a code would be 42883
		if f.name == "interface" && f.v != nil && !rest.IsUUID(*f.v) {
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "interface must be a UUID"))
			return
		}
	}
	if hasOperDate && string(b.OperDate) != "null" {
		var ts time.Time
		if json.Unmarshal(b.OperDate, &ts) != nil {
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "oper_date must be an RFC 3339 timestamp or null"))
			return
		}
		operDate = &ts
	}
	if b.Area == nil && b.Interface == nil && b.Locale == nil && !hasOperDate {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "nothing to set: one of area, interface, locale, oper_date"))
		return
	}
	var row json.RawMessage
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, raw), func(ctx context.Context, tx pgx.Tx) error {
		for _, f := range fields {
			if f.v == nil {
				continue
			}
			fn, args := sessionCall(f.name, *f.v)
			if _, err := pgtx.Call(ctx, tx, fn, args); err != nil {
				return err
			}
		}
		if hasOperDate {
			if _, err := pgtx.Call(ctx, tx, "set_session_oper_date", pgtx.Args{"operdate": pgtx.Typed{V: operDate, Type: "timestamptz"}}); err != nil {
				return err
			}
		}
		var err error
		row, err = readMe(ctx, tx)
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	rest.WriteJSON(w, 200, row)
}
