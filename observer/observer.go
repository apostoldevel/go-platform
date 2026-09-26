// Package observer is the GoAPI package of the SQL module observer
// (db/sql/platform/observer): the publishers of the pub/sub and the
// caller's subscriptions to them, as /api/v2/observer — v1 rest.observer
// without subscribe/unsubscribe. A subscription lives in a WebSocket
// session (events over WebSocket, requests over HTTP), and its key is
// a session code, which is a credential and stays off the wire: v2 reads
// the listeners of the caller's own session only, without the code, and
// writes none.
package observer

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"

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

const prefix = "/api/v2/observer"

// publishers are a dozen rows keyed by code: the whole view as an array.
// api.count_publisher is unusable (count(id) on a table without id).
const publishers = prefix + "/publishers"

// codeRe is a publisher code or a listener identity: a NOTIFY channel name.
var codeRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func codeOf(r *http.Request, name string) (string, error) {
	v := r.PathValue(name)
	if !codeRe.MatchString(v) {
		return "", problem.New(400, "validation", "Bad request", name+" must be a code")
	}
	return v, nil
}

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger}
}

func (m *module) Name() string       { return "observer" }
func (m *module) Prefixes() []string { return []string{prefix} }

// Routes registers the publishers and the caller's listeners on the mux.
func (m *module) Routes(mux *http.ServeMux) {
	d, log := m.cfg.Doer, m.log
	mux.HandleFunc("GET "+publishers, rest.CallRowsHandler(d, log, "list_publisher", pgtx.Args{"orderby": json.RawMessage(`["code ASC"]`), "limit": 0}))
	mux.HandleFunc("GET "+publishers+"/{code}", rest.CallRowHandler(d, log, "get_publisher", func(r *http.Request) (pgtx.Args, error) {
		code, err := codeOf(r, "code")
		return pgtx.Args{"code": code}, err
	}))
	// the caller's own listeners: api.list_my_listener / get_my_listener
	// (db-platform 1.2.31) take the session from the context the database
	// opened, never from the request — and do not answer the session code
	mux.HandleFunc("GET "+prefix+"/listeners", func(w http.ResponseWriter, r *http.Request) {
		var rows []json.RawMessage
		err := d.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) (err error) {
			rows, err = pgtx.Call(ctx, tx, "list_my_listener", nil)
			return err
		})
		if err != nil {
			rest.Fail(w, r, log, err)
			return
		}
		for i, row := range rows {
			if rows[i], err = listenerOf(row); err != nil {
				rest.Fail(w, r, log, err)
				return
			}
		}
		body, _ := json.Marshal(rows)
		rest.WriteJSON(w, 200, body)
	})
	mux.HandleFunc("GET "+prefix+"/listeners/{publisher}/{identity}", rest.RowFunc(d, log, func(r *http.Request) (pgtx.Args, error) {
		publisher, err := codeOf(r, "publisher")
		if err != nil {
			return nil, err
		}
		identity, err := codeOf(r, "identity")
		if err != nil {
			return nil, err
		}
		return pgtx.Args{"publisher": publisher, "identity": identity}, nil
	}, func(ctx context.Context, tx pgx.Tx, a pgtx.Args) (json.RawMessage, error) {
		row, err := pgtx.CallRow(ctx, tx, "get_my_listener", a)
		if err != nil {
			return nil, err
		}
		return listenerOf(row)
	}))
}

// listenerOf is the listener as the API answers it: publisher, identity,
// filter, params — the session code never leaves the database's row.
func listenerOf(row json.RawMessage) (json.RawMessage, error) {
	var l struct {
		Publisher json.RawMessage `json:"publisher"`
		Identity  json.RawMessage `json:"identity"`
		Filter    json.RawMessage `json:"filter"`
		Params    json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(row, &l); err != nil {
		return nil, err // never the row itself: it carries the session code
	}
	null := json.RawMessage("null")
	orNull := func(v json.RawMessage) json.RawMessage {
		if v == nil {
			return null
		}
		return v
	}
	return json.Marshal(map[string]json.RawMessage{"publisher": orNull(l.Publisher), "identity": orNull(l.Identity), "filter": orNull(l.Filter), "params": orNull(l.Params)})
}
