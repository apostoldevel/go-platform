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
	"log/slog"
	"net/http"
	"regexp"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
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
	mux.HandleFunc("GET "+publishers, rest.RowsHandler(d, log, "SELECT row_to_json(t) FROM api.publisher t ORDER BY code"))
	mux.HandleFunc("GET "+publishers+"/{code}", rest.RowHandler(d, log,
		"SELECT row_to_json(t) FROM api.get_publisher($1) t", func(r *http.Request) ([]any, error) {
			code, err := codeOf(r, "code")
			return []any{code}, err
		}))
	// the session is the caller's, taken from the request, never from the path
	const listener = "json_build_object('publisher', publisher, 'identity', identity, 'filter', filter, 'params', params)"
	mux.HandleFunc("GET "+prefix+"/listeners", func(w http.ResponseWriter, r *http.Request) {
		rest.RowsHandler(d, log, "SELECT "+listener+" FROM api.listener WHERE session = $1 ORDER BY publisher, identity", platform.SessionOf(r).Code)(w, r)
	})
	mux.HandleFunc("GET "+prefix+"/listeners/{publisher}/{identity}", rest.RowHandler(d, log,
		"SELECT "+listener+" FROM api.get_listener($1, $2, $3)", func(r *http.Request) ([]any, error) {
			publisher, err := codeOf(r, "publisher")
			if err != nil {
				return nil, err
			}
			identity, err := codeOf(r, "identity")
			if err != nil {
				return nil, err
			}
			return []any{publisher, platform.SessionOf(r).Code, identity}, nil
		}))
}
