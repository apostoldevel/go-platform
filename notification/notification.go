// Package notification is the GoAPI package of the SQL module notification
// (db/sql/platform/notification): the journal of state changes delivered to
// the caller, as /api/v2/notifications — v1 rest.notification. Beside the
// list and one row: /since?from= (v1 /notification {point}) and /changed —
// the objects behind the notifications of a period, each read through its
// entity's api.get_<entity> (v1 /notification/changed/objects).
package notification

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
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

var notifications = rest.Resource{Prefix: "/api/v2/notifications", GetFn: "api.get_notification", ListFn: "api.list_notification", CountFn: "api.count_notification"}

// maxObjects bounds ?objects= of /changed: one request, one page of objects.
const maxObjects = 100

var identRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// getFnOf is api.get_<entity> for an entity code the database gave back —
// the code is interpolated into SQL, so only a plain identifier passes.
func getFnOf(code string) (string, bool) {
	if !identRe.MatchString(code) {
		return "", false
	}
	return "api.get_" + code, true
}

// timeParam reads an RFC 3339 query parameter; absent is nil.
func timeParam(r *http.Request, name string) (*time.Time, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return nil, nil
	}
	ts, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, problem.New(400, "validation", "Bad request", name+" must be an RFC 3339 timestamp")
	}
	return &ts, nil
}

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger}
}

func (m *module) Name() string       { return "notification" }
func (m *module) Prefixes() []string { return []string{notifications.Prefix} }

// Routes registers the journal and its two views on the shared mux.
func (m *module) Routes(mux *http.ServeMux) {
	p := notifications.Prefix
	mux.HandleFunc("GET "+p, notifications.List(m.cfg.Doer, m.log))
	mux.HandleFunc("GET "+p+"/{id}", notifications.Get(m.cfg.Doer, m.log))
	mux.HandleFunc("GET "+p+"/since", rest.CallRowsOf(m.cfg.Doer, m.log, "notification", sinceArgs))
	mux.HandleFunc("GET "+p+"/changed", m.changed)
}

// sinceArgs is ?from= of /notifications/since: the caller's notifications
// from a point in time (default now), as api.notification(from) gives them.
func sinceArgs(r *http.Request) (pgtx.Args, error) {
	from, err := timeParam(r, "from")
	if err != nil {
		return nil, err
	}
	if from == nil {
		now := time.Now()
		from = &now
	}
	return pgtx.Args{"datefrom": *from}, nil
}

// changed is GET /notifications/changed?objects=a,b&from=&to=: the distinct
// objects notified about in the period, each as its entity's api.get_<x>
// row; an object the caller may not read is left out, as in v1.
func (m *module) changed(w http.ResponseWriter, r *http.Request) {
	var objects []string
	for _, o := range strings.Split(r.URL.Query().Get("objects"), ",") {
		if o = strings.TrimSpace(o); o == "" {
			continue
		}
		if !rest.IsUUID(o) {
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "objects must be a comma-separated list of UUIDs"))
			return
		}
		objects = append(objects, o)
	}
	if len(objects) == 0 || len(objects) > maxObjects {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "objects: 1 to 100 UUIDs are required"))
		return
	}
	from, err := timeParam(r, "from")
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	to, err := timeParam(r, "to")
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	// the v1 search: object IN objects, datetime ≥ from, datetime < to
	search := []map[string]any{{"field": "object", "valarr": objects, "compare": "IN"}}
	if from != nil {
		search = append(search, map[string]any{"field": "datetime", "compare": "GEQ", "value": from.Format(time.RFC3339Nano)})
	}
	if to != nil {
		search = append(search, map[string]any{"field": "datetime", "compare": "LSS", "value": to.Format(time.RFC3339Nano)})
	}
	searchJSON, _ := json.Marshal(search)
	out := []json.RawMessage{}
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		notes, err := pgtx.Call(ctx, tx, "list_notification", pgtx.Args{"search": json.RawMessage(searchJSON)})
		if err != nil {
			return err
		}
		// the distinct (entity, object) pairs, in the order first seen
		type pair struct {
			Entity string `json:"entitycode"`
			Object string `json:"object"`
		}
		seen := map[pair]bool{}
		for _, n := range notes {
			var p pair
			if err := json.Unmarshal(n, &p); err != nil {
				return err
			}
			if seen[p] {
				continue
			}
			seen[p] = true
			fn, ok := getFnOf(p.Entity)
			if !ok {
				continue
			}
			rows, err := pgtx.Call(ctx, tx, fn, pgtx.Args{"id": p.Object})
			if err != nil {
				return err
			}
			// a row the caller may not read comes back empty or with a null id
			for _, row := range rows {
				var id struct {
					ID *string `json:"id"`
				}
				if json.Unmarshal(row, &id) == nil && id.ID != nil {
					out = append(out, row)
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	body, _ := json.Marshal(out)
	rest.WriteJSON(w, 200, body)
}
