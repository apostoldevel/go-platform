// Package log is the GoAPI package of the SQL module log
// (db/sql/platform/log): the event log — every row WriteToEventLog wrote —
// as /api/v2/event-log (all of it, what the administrator sees; v1
// /admin/event/log/*) and /api/v2/me/event-log (the current user's rows;
// v1 /event/log/*). Rows are journal entries: the id is a bigint, there is
// no PATCH and no DELETE, POST writes one row through api.write_to_log.
package log

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
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

var (
	eventLog = rest.Resource{Prefix: "/api/v2/event-log", GetFn: "api.get_event_log", ListFn: "api.list_event_log", CountFn: "api.count_event_log", IntID: true}
	myLog    = rest.Resource{Prefix: "/api/v2/me/event-log", GetFn: "api.get_user_log", ListFn: "api.list_user_log", CountFn: "api.count_user_log", IntID: true}
)

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger, idem: rest.NewIdempotency(24 * time.Hour)}
}

func (m *module) Name() string       { return "log" }
func (m *module) Prefixes() []string { return []string{eventLog.Prefix, myLog.Prefix} }

// Routes registers the two journals.
func (m *module) Routes(mux *http.ServeMux) {
	for _, res := range []rest.Resource{eventLog, myLog} {
		mux.HandleFunc("GET "+res.Prefix, res.List(m.cfg.Doer, m.log))
		mux.HandleFunc("GET "+res.Prefix+"/{id}", res.Get(m.cfg.Doer, m.log))
	}
	mux.HandleFunc("POST "+eventLog.Prefix, m.write)
}

// entry is what api.write_to_log takes: type M/W/E/D, code, scope, text.
type entry struct {
	Type  string  `json:"type"`
	Code  *int    `json:"code"`
	Scope *string `json:"scope"`
	Text  string  `json:"text"`
}

func (m *module) write(w http.ResponseWriter, r *http.Request) {
	var e entry
	raw, err := rest.ReadBody(r, &e)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	switch e.Type {
	case "M", "W", "E", "D":
	default:
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "type must be one of M, W, E, D"))
		return
	}
	if e.Text == "" {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "text is required"))
		return
	}
	// a journal write is not idempotent by itself — a retried POST is a second row without the key
	rest.Once(w, r, m.idem, platform.SessionOf(r).Code, raw, m.log, func(w http.ResponseWriter) {
		var row json.RawMessage
		err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 201, raw), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, "SELECT row_to_json(t) FROM api.write_to_log($1, $2::integer, $3, $4) t", e.Type, e.Code, e.Scope, e.Text).Scan(&row)
		})
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		var written struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(row, &written)
		if written.ID > 0 {
			w.Header().Set("Location", eventLog.Prefix+"/"+strconv.FormatInt(written.ID, 10))
		}
		rest.WriteJSON(w, 201, row)
	})
}
