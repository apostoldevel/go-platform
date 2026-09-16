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
	"log/slog"
	"net/http"
	"time"

	platform "github.com/apostoldevel/go-platform"
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

// meSQL is the seven v1 reads as one row.
const meSQL = `SELECT json_build_object(
  'user',      (SELECT row_to_json(u) FROM api.current_user() u),
  'area',      (SELECT row_to_json(a) FROM api.current_area() a),
  'interface', (SELECT row_to_json(i) FROM api.current_interface() i),
  'locale',    (SELECT row_to_json(l) FROM api.current_locale() l),
  'oper_date', api.oper_date())`

// body is what PATCH /me may set. area, interface and locale take a uuid or
// a code (the api.* overloads); oper_date is RFC 3339, null clears it.
type body struct {
	Area      *string         `json:"area"`
	Interface *string         `json:"interface"`
	Locale    *string         `json:"locale"`
	OperDate  json.RawMessage `json:"oper_date"` // "null" when given as null, nil when absent
}

// castOf picks the api.set_session_<x> overload by the value's shape.
func castOf(v string) string {
	if rest.IsUUID(v) {
		return "::uuid"
	}
	return "::text"
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
		return tx.QueryRow(ctx, meSQL).Scan(&row)
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
			if _, err := tx.Exec(ctx, "SELECT api.set_session_"+f.name+"($1"+castOf(*f.v)+")", *f.v); err != nil {
				return err
			}
		}
		if hasOperDate {
			if _, err := tx.Exec(ctx, "SELECT api.set_session_oper_date($1::timestamptz)", operDate); err != nil {
				return err
			}
		}
		return tx.QueryRow(ctx, meSQL).Scan(&row)
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	rest.WriteJSON(w, 200, row)
}
