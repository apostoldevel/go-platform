// Package verification is the GoAPI package of the SQL module verification
// (db/sql/platform/verification): one-time codes for confirming an e-mail or
// a phone, as /api/v2/verification/codes — v1 rest.verification. The four v1
// paths email|phone × code|confirm are one POST each with the channel in the
// body: {type: email|phone}.
package verification

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
	cfg  Config
	log  *slog.Logger
	idem *rest.Idempotency
}

const prefix = "/api/v2/verification"

var codes = rest.Resource{Prefix: prefix + "/codes", GetFn: "api.get_verification_code", ListFn: "api.list_verification_code", CountFn: "api.count_verification_code"}

// kindOf maps the wire word to the database's channel letter.
func kindOf(t string) (string, bool) {
	switch t {
	case "email":
		return "M", true
	case "phone":
		return "P", true
	}
	return "", false
}

// body is a code request or its confirmation: the channel and, for
// confirmation or an explicit value, the code.
type body struct {
	Type string  `json:"type"`
	Code *string `json:"code"`
}

func (m *module) read(r *http.Request, needCode bool) (body, []byte, error) {
	var b body
	raw, err := rest.ReadBody(r, &b)
	if err != nil {
		return b, raw, err
	}
	if _, ok := kindOf(b.Type); !ok {
		return b, raw, problem.New(400, "validation", "Bad request", "type must be email or phone")
	}
	if (b.Code != nil && *b.Code == "") || (needCode && b.Code == nil) {
		return b, raw, problem.New(400, "validation", "Bad request", "code is required")
	}
	return b, raw, nil
}

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger, idem: rest.NewIdempotency(24 * time.Hour)}
}

func (m *module) Name() string       { return "verification" }
func (m *module) Prefixes() []string { return []string{prefix} }

// Routes registers the codes and their two actions on the shared mux.
func (m *module) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+codes.Prefix, codes.List(m.cfg.Doer, m.log))
	mux.HandleFunc("GET "+codes.Prefix+"/{id}", codes.Get(m.cfg.Doer, m.log))
	mux.HandleFunc("POST "+codes.Prefix, m.create)
	mux.HandleFunc("POST "+codes.Prefix+"/confirm", m.confirm)
}

// create is POST /verification/codes {type, code?}: a new code for the
// caller — generated when not given — as 201 with the row.
func (m *module) create(w http.ResponseWriter, r *http.Request) {
	b, raw, err := m.read(r, false)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	kind, _ := kindOf(b.Type)
	rest.Once(w, r, m.idem, platform.SessionOf(r).Code, raw, m.log, func(w http.ResponseWriter) {
		var row json.RawMessage
		err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 201, raw), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT row_to_json(t) FROM api.new_verification_code($1::char, $2) t", kind, b.Code).Scan(&row)
		})
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		if loc := rest.LocationOf(codes, row); loc != "" {
			w.Header().Set("Location", loc)
		}
		rest.SetETag(w, row)
		rest.WriteJSON(w, 201, row)
	})
}

// confirm is POST /verification/codes/confirm {type, code}: the code is
// spent — 200 {confirmed: true}; a wrong or used code is 400 with the
// database's reason, as in v1's {result: false, message}.
func (m *module) confirm(w http.ResponseWriter, r *http.Request) {
	b, raw, err := m.read(r, true)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	kind, _ := kindOf(b.Type)
	var ok bool
	var message *string
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, raw), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT result, message FROM api.confirm_verification_code($1::char, $2)", kind, *b.Code).Scan(&ok, &message); err != nil {
			return err
		}
		if !ok {
			detail := "the code is not valid"
			if message != nil && *message != "" {
				detail = *message
			}
			return problem.New(400, "verification", "Bad request", detail)
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	rest.WriteJSON(w, 200, []byte(`{"confirmed":true}`))
}
