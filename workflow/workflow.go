// Package workflow is the GoAPI package of the SQL module workflow
// (db/sql/platform/workflow): the automaton as data — entities, types,
// classes, states, actions, methods, transitions, events, priorities — as
// /api/v2 resources over the module's api.* (v1 rest.workflow, rest.state,
// rest.action, rest.method and the catalogue branches of rest.api). The
// catalogues seeded by the platform (entities, actions, priorities, state
// and event types) are read-only; the rest is the constructor's material.
package workflow

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

// read-only catalogues: api.get_/list_/count_ only
var (
	entities   = rest.Resource{Prefix: "/api/v2/entities", GetFn: "api.get_entity", ListFn: "api.list_entity", CountFn: "api.count_entity"}
	actions    = rest.Resource{Prefix: "/api/v2/actions", GetFn: "api.get_action", ListFn: "api.list_action", CountFn: "api.count_action"}
	priorities = rest.Resource{Prefix: "/api/v2/priorities", GetFn: "api.get_priority", ListFn: "api.list_priority", CountFn: "api.count_priority"}
	// the two type catalogues have a get by id and a view, no list function
	stateTypes = rest.Resource{Prefix: "/api/v2/state-types", GetFn: "api.get_state_type"}
	eventTypes = rest.Resource{Prefix: "/api/v2/event-types", GetFn: "api.get_event_type"}
)

// the constructor's material: api.set_<x> / api.delete_<x>
var (
	types = rest.Writable{
		Resource: rest.Resource{Prefix: "/api/v2/types", GetFn: "api.get_type", ListFn: "api.list_type", CountFn: "api.count_type"},
		SetSQL:   "SELECT row_to_json(t) FROM api.set_type($1::uuid, $2::uuid, $3, $4, $5) t",
		NewBody:  func() rest.Body { return &typeBody{} },
		DeleteFn: "api.delete_type",
	}
	classes = rest.Writable{
		Resource: rest.Resource{Prefix: "/api/v2/classes", GetFn: "api.get_class", ListFn: "api.list_class", CountFn: "api.count_class"},
		SetSQL:   "SELECT row_to_json(t) FROM api.set_class($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::boolean) t",
		NewBody:  func() rest.Body { return &classBody{} },
		DeleteFn: "api.delete_class",
	}
	states = rest.Writable{
		Resource: rest.Resource{Prefix: "/api/v2/states", GetFn: "api.get_state", ListFn: "api.list_state", CountFn: "api.count_state"},
		SetSQL:   "SELECT row_to_json(t) FROM api.set_state($1::uuid, $2::uuid, $3::uuid, $4, $5, $6::integer) t",
		NewBody:  func() rest.Body { return &stateBody{} },
		DeleteFn: "api.delete_state",
	}
	methods = rest.Writable{
		Resource: rest.Resource{Prefix: "/api/v2/methods", GetFn: "api.get_method", ListFn: "api.list_method", CountFn: "api.count_method"},
		SetSQL:   "SELECT row_to_json(t) FROM api.set_method($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6, $7, $8::integer, $9::boolean) t",
		NewBody:  func() rest.Body { return &methodBody{} },
		DeleteFn: "api.delete_method",
	}
	transitions = rest.Writable{
		Resource: rest.Resource{Prefix: "/api/v2/transitions", GetFn: "api.get_transition", ListFn: "api.list_transition", CountFn: "api.count_transition"},
		SetSQL:   "SELECT row_to_json(t) FROM api.set_transition($1::uuid, $2::uuid, $3::uuid, $4::uuid) t",
		NewBody:  func() rest.Body { return &transitionBody{} },
		DeleteFn: "api.delete_transition",
	}
	events = rest.Writable{
		Resource: rest.Resource{Prefix: "/api/v2/events", GetFn: "api.get_event", ListFn: "api.list_event", CountFn: "api.count_event"},
		SetSQL:   "SELECT row_to_json(t) FROM api.set_event($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7::integer, $8::boolean) t",
		NewBody:  func() rest.Body { return &eventBody{} },
		DeleteFn: "api.delete_event",
	}
)

// ── bodies: the parameters of api.set_<x> after the id ─────────────────

type typeBody struct {
	Class       *string `json:"class"`
	Code        *string `json:"code"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (b *typeBody) Validate(create bool) error {
	return rest.RequiredAll(create, rest.Req("code", b.Code), rest.Req("class", b.Class))
}
func (b *typeBody) Args(id any) []any { return []any{id, b.Class, b.Code, b.Name, b.Description} }

type classBody struct {
	Parent   *string `json:"parent"`
	Entity   *string `json:"entity"`
	Code     *string `json:"code"`
	Label    *string `json:"label"`
	Abstract *bool   `json:"abstract"`
}

func (b *classBody) Validate(create bool) error {
	return rest.RequiredAll(create, rest.Req("code", b.Code), rest.Req("parent", b.Parent), rest.Req("entity", b.Entity))
}
func (b *classBody) Args(id any) []any {
	abstract := true // api.set_class defaults pAbstract to true; absent keeps the default
	if b.Abstract != nil {
		abstract = *b.Abstract
	}
	return []any{id, b.Parent, b.Entity, b.Code, b.Label, abstract}
}

type stateBody struct {
	Class    *string `json:"class"`
	Type     *string `json:"type"`
	Code     *string `json:"code"`
	Label    *string `json:"label"`
	Sequence *int    `json:"sequence"`
}

func (b *stateBody) Validate(create bool) error {
	return rest.RequiredAll(create, rest.Req("code", b.Code), rest.Req("class", b.Class), rest.Req("type", b.Type))
}
func (b *stateBody) Args(id any) []any {
	return []any{id, b.Class, b.Type, b.Code, b.Label, b.Sequence}
}

type methodBody struct {
	Parent   *string `json:"parent"`
	Class    *string `json:"class"`
	State    *string `json:"state"`
	Action   *string `json:"action"`
	Code     *string `json:"code"`
	Label    *string `json:"label"`
	Sequence *int    `json:"sequence"`
	Visible  *bool   `json:"visible"`
}

func (b *methodBody) Validate(create bool) error {
	return rest.RequiredAll(create, rest.Req("class", b.Class), rest.Req("state", b.State), rest.Req("action", b.Action))
}
func (b *methodBody) Args(id any) []any {
	return []any{id, b.Parent, b.Class, b.State, b.Action, b.Code, b.Label, b.Sequence, b.Visible}
}

type transitionBody struct {
	State    *string `json:"state"`
	Method   *string `json:"method"`
	NewState *string `json:"newstate"`
}

func (b *transitionBody) Validate(create bool) error {
	return rest.RequiredAll(create, rest.Req("state", b.State), rest.Req("method", b.Method), rest.Req("newstate", b.NewState))
}
func (b *transitionBody) Args(id any) []any { return []any{id, b.State, b.Method, b.NewState} }

type eventBody struct {
	Class    *string `json:"class"`
	Type     *string `json:"type"`
	Action   *string `json:"action"`
	Label    *string `json:"label"`
	Text     *string `json:"text"`
	Sequence *int    `json:"sequence"`
	Enabled  *bool   `json:"enabled"`
}

func (b *eventBody) Validate(create bool) error {
	return rest.RequiredAll(create, rest.Req("class", b.Class), rest.Req("type", b.Type), rest.Req("action", b.Action))
}
func (b *eventBody) Args(id any) []any {
	return []any{id, b.Class, b.Type, b.Action, b.Label, b.Text, b.Sequence, b.Enabled}
}

// ── module ─────────────────────────────────────────────────────────────

// New returns the package as a platform.Module.
func New(cfg Config) platform.Module {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &module{cfg: cfg, log: cfg.Logger, idem: rest.NewIdempotency(24 * time.Hour)}
}

func (m *module) Name() string { return "workflow" }
func (m *module) Prefixes() []string {
	return []string{entities.Prefix, types.Prefix, classes.Prefix, states.Prefix, stateTypes.Prefix, actions.Prefix, methods.Prefix, transitions.Prefix, events.Prefix, eventTypes.Prefix, priorities.Prefix}
}

// Routes registers the resources on the shared mux.
func (m *module) Routes(mux *http.ServeMux) {
	d, log := m.cfg.Doer, m.log
	for _, ro := range []rest.Resource{entities, actions, priorities} {
		mux.HandleFunc("GET "+ro.Prefix, ro.List(d, log))
		mux.HandleFunc("GET "+ro.Prefix+"/{id}", ro.Get(d, log))
	}
	for _, ct := range []struct {
		res  rest.Resource
		view string
	}{{stateTypes, "api.state_type"}, {eventTypes, "api.event_type"}} {
		mux.HandleFunc("GET "+ct.res.Prefix, rest.RowsHandler(d, log, "SELECT row_to_json(t) FROM "+ct.view+" t")) // the view is read as the pool's role: without GRANT SELECT to it, 500
		mux.HandleFunc("GET "+ct.res.Prefix+"/{id}", ct.res.Get(d, log))
	}
	for _, rw := range []rest.Writable{types, classes, states, methods, transitions, events} {
		rw.Routes(mux, d, m.idem, log)
	}
	mux.HandleFunc("POST "+classes.Prefix+"/{id}/actions/{action}", m.classAction)
	rest.Access{Resource: classes.Resource, ListFn: "api.class_access", DecodeFn: "api.decode_class_access", MaxMask: rest.MaskBits10, ClassOptions: true, Set: m.chmodc}.Routes(mux, m.cfg.Doer, m.log)
	rest.Access{Resource: methods.Resource, ListFn: "api.method_access", DecodeFn: "api.decode_method_access", MaxMask: rest.MaskBits6, Set: m.chmodm}.Routes(mux, m.cfg.Doer, m.log)
}

// classAction is POST /classes/{id}/actions/{action}: copy {destination} —
// api.copy_class(id, destination) → 204; clone {entity, code, label,
// abstract} — api.clone_class(id, …) → 201 with the new class. The set is closed.
func (m *module) classAction(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	switch r.PathValue("action") {
	case "copy":
		var b struct {
			Destination string `json:"destination"`
		}
		raw, err := rest.ReadBody(r, &b)
		if err == nil && !rest.IsUUID(b.Destination) {
			err = problem.New(400, "validation", "Bad request", "destination class id is required")
		}
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		if err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 204, raw), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := classes.GetRow(ctx, tx, id); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "SELECT api.copy_class($1::uuid, $2::uuid)", id, b.Destination)
			return err
		}); err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		w.WriteHeader(204)
	case "clone":
		var b classBody
		raw, err := rest.ReadBody(r, &b)
		if err == nil {
			if err = rest.Required("code", b.Code, true); err == nil {
				err = rest.Required("entity", b.Entity, true)
			}
		}
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		abstract := false // api.clone_class defaults pAbstract to false
		if b.Abstract != nil {
			abstract = *b.Abstract
		}
		var row json.RawMessage
		if err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 201, raw), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := classes.GetRow(ctx, tx, id); err != nil {
				return err
			}
			return tx.QueryRow(ctx, "SELECT row_to_json(t) FROM api.clone_class($1::uuid, $2::uuid, $3, $4, $5::boolean) t", id, b.Entity, b.Code, b.Label, abstract).Scan(&row)
		}); err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		var created struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(row, &created)
		w.Header().Set("Location", classes.Prefix+"/"+created.ID)
		rest.SetETag(w, row)
		rest.WriteJSON(w, 201, row)
	default:
		rest.Fail(w, r, m.log, problem.New(404, "not-found", "Not found", "no such action"))
	}
}

// ── access: class rights (ACU) and method rights (AMU) ─────────────────
// The routes are rest.Access (shared with the object's AOU); here only
// what api.chmodc / chmodm take — the mask's width is bounded there.

func (m *module) chmodc(ctx context.Context, tx pgx.Tx, id string, b rest.AccessBody) error {
	recursive, objectSet := true, false // the defaults of api.chmodc
	if b.Recursive != nil {
		recursive = *b.Recursive
	}
	if b.ObjectSet != nil {
		objectSet = *b.ObjectSet
	}
	_, err := tx.Exec(ctx, "SELECT api.chmodc($1::uuid, $2::int, $3::uuid, $4::boolean, $5::boolean)", id, *b.Mask, b.UserID, recursive, objectSet)
	return err
}

func (m *module) chmodm(ctx context.Context, tx pgx.Tx, id string, b rest.AccessBody) error {
	_, err := tx.Exec(ctx, "SELECT api.chmodm($1::uuid, $2::int, $3::uuid)", id, *b.Mask, b.UserID)
	return err
}
