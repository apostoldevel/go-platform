package admin

import (
	"context"
	"encoding/json"
	"net/http"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
	"github.com/jackc/pgx/v5"
)

// body is the writable part of a collection's row: the parameters of its
// api.set_<x>, in order, after the id. validate(create) — on create the
// required field must be present, on PATCH it may be absent (kept) but not
// emptied.
type body interface {
	validate(create bool) error
	args(id any) []any
}

// required is the one rule both ways: present and empty is always wrong,
// absent is wrong only on create.
func required(name string, v *string, create bool) error {
	if (v == nil && create) || (v != nil && *v == "") {
		return problem.New(400, "validation", "Bad request", name+" is required")
	}
	return nil
}

// collection is one of the admin module's "named things users belong to":
// groups, areas, interfaces — api.set_<x>/delete_<x> and the members
// api.<x>_member / <x>_member_add / <x>_member_delete. The SQL of add and
// delete is spelled out per collection because one of them differs:
// api.interface_member_add takes (member, interface), every other add and
// every delete takes (collection, member).
type collection struct {
	res       rest.Resource
	setSQL    string
	newBody   func() body
	deleteFn  string
	membersFn string
	addSQL    string // $1 = collection id, $2 = user id
	delSQL    string
}

var groups = collection{
	res:       rest.Resource{Prefix: "/api/v2/groups", GetFn: "api.get_group", ListFn: "api.list_group", CountFn: "api.count_group"},
	setSQL:    "SELECT row_to_json(t) FROM api.set_group($1::uuid, $2, $3, $4) t",
	newBody:   func() body { return &groupBody{} },
	deleteFn:  "api.delete_group",
	membersFn: "api.group_member",
	addSQL:    "SELECT api.group_member_add($1::uuid, $2::uuid)",
	delSQL:    "SELECT api.group_member_delete($1::uuid, $2::uuid)",
}

var areas = collection{
	res:       rest.Resource{Prefix: "/api/v2/areas", GetFn: "api.get_area", ListFn: "api.list_area", CountFn: "api.count_area"},
	setSQL:    "SELECT row_to_json(t) FROM api.set_area($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, $7, $8::integer, $9::timestamptz, $10::timestamptz) t",
	newBody:   func() body { return &areaBody{} },
	deleteFn:  "api.delete_area",
	membersFn: "api.area_member",
	addSQL:    "SELECT api.area_member_add($1::uuid, $2::uuid)",
	delSQL:    "SELECT api.area_member_delete($1::uuid, $2::uuid)",
}

var interfaces = collection{
	res:       rest.Resource{Prefix: "/api/v2/interfaces", GetFn: "api.get_interface", ListFn: "api.list_interface", CountFn: "api.count_interface"},
	setSQL:    "SELECT row_to_json(t) FROM api.set_interface($1::uuid, $2, $3, $4) t",
	newBody:   func() body { return &interfaceBody{} },
	deleteFn:  "api.delete_interface",
	membersFn: "api.interface_member",
	addSQL:    "SELECT api.interface_member_add($2::uuid, $1::uuid)", // (member, interface)
	delSQL:    "SELECT api.interface_member_delete($1::uuid, $2::uuid)",
}

// areaTypes is the view api.area_type (v1 /admin/area/type); sessions is
// api.list_session/count_session (v1 /admin/session/*) — read-only: a session
// code is a credential and is never addressed in a path.
var (
	areaTypes = rest.Resource{Prefix: "/api/v2/area-types"}
	sessions  = rest.Resource{Prefix: "/api/v2/sessions", ListFn: "api.list_session", CountFn: "api.count_session"}
)

type groupBody struct {
	Username    *string `json:"username"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (b *groupBody) validate(create bool) error { return required("username", b.Username, create) }
func (b *groupBody) args(id any) []any          { return []any{id, b.Username, b.Name, b.Description} }

type areaBody struct {
	Parent        *string `json:"parent"`
	Type          *string `json:"type"`
	Scope         *string `json:"scope"`
	Code          *string `json:"code"`
	Name          *string `json:"name"`
	Description   *string `json:"description"`
	Sequence      *int    `json:"sequence"`
	ValidFromDate *string `json:"valid_from_date"`
	ValidToDate   *string `json:"valid_to_date"`
}

func (b *areaBody) validate(create bool) error { return required("code", b.Code, create) }
func (b *areaBody) args(id any) []any {
	return []any{id, b.Parent, b.Type, b.Scope, b.Code, b.Name, b.Description, b.Sequence, b.ValidFromDate, b.ValidToDate}
}

type interfaceBody struct {
	Code        *string `json:"code"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (b *interfaceBody) validate(create bool) error { return required("code", b.Code, create) }
func (b *interfaceBody) args(id any) []any          { return []any{id, b.Code, b.Name, b.Description} }

func (m *module) collectionRoutes(mux *http.ServeMux) {
	for _, c := range []collection{groups, areas, interfaces} {
		p := c.res.Prefix
		mux.HandleFunc("GET "+p, c.res.List(m.cfg.Doer, m.log))
		mux.HandleFunc("GET "+p+"/{id}", c.res.Get(m.cfg.Doer, m.log))
		mux.HandleFunc("POST "+p, m.collectionCreate(c))
		mux.HandleFunc("PATCH "+p+"/{id}", m.collectionUpdate(c))
		mux.HandleFunc("DELETE "+p+"/{id}", m.collectionDelete(c))
		mux.HandleFunc("GET "+p+"/{id}/members", m.membersList(c))
		mux.HandleFunc("POST "+p+"/{id}/members", m.membersAdd(c))
		mux.HandleFunc("DELETE "+p+"/{id}/members/{uid}", m.membersDelete(c))
	}
	mux.HandleFunc("POST "+areas.res.Prefix+"/{id}/actions/{action}", m.areaAction)
	mux.HandleFunc("POST "+areas.res.Prefix+"/actions/clear", m.areasClear)
	mux.HandleFunc("GET "+areaTypes.Prefix, m.view("api.area_type"))
	mux.HandleFunc("GET "+sessions.Prefix, sessions.List(m.cfg.Doer, m.log))
}

// view is GET of a whole api.<view> (the v1 branches that read a view with
// `fields`), as a plain array.
func (m *module) view(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var rows []json.RawMessage
		err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) (err error) {
			rows, err = rest.Rows(ctx, tx, "SELECT row_to_json(t) FROM "+name+" t")
			return err
		})
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		body, _ := json.Marshal(rows)
		rest.WriteJSON(w, 200, body)
	}
}

func (m *module) collectionCreate(c collection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b := c.newBody()
		raw, err := rest.ReadBody(r, b)
		if err == nil {
			err = b.validate(true)
		}
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		if key != "" {
			if rec, conflict := m.idem.Lookup(rest.IdemScope(r, platform.SessionOf(r).Code), key, raw); conflict {
				rest.Fail(w, r, m.log, problem.New(409, "conflict", "Conflict", "Idempotency-Key reused with a different body"))
				return
			} else if rec != nil {
				rec.Replay(w)
				return
			}
		}
		cw := &rest.Capture{ResponseWriter: w}
		var row json.RawMessage
		err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 201, raw), func(ctx context.Context, tx pgx.Tx) error {
			return tx.QueryRow(ctx, c.setSQL, b.args(nil)...).Scan(&row)
		})
		if err != nil {
			rest.Fail(cw, r, m.log, err)
		} else {
			var created struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(row, &created)
			cw.Header().Set("Location", c.res.Prefix+"/"+created.ID)
			rest.SetETag(cw, row)
			rest.WriteJSON(cw, 201, row)
		}
		if key != "" && cw.Status < 500 {
			m.idem.Store(rest.IdemScope(r, platform.SessionOf(r).Code), key, raw, cw)
		}
	}
}

func (m *module) collectionUpdate(c collection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := rest.IDOf(r)
		if err == nil {
			err = rest.Precondition(r)
		}
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		b := c.newBody()
		raw, err := rest.ReadBody(r, b)
		if err == nil {
			err = b.validate(false)
		}
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		var row json.RawMessage
		err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, raw), func(ctx context.Context, tx pgx.Tx) error {
			current, err := c.res.GetRow(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := rest.IfMatch(r, current); err != nil {
				return err
			}
			return tx.QueryRow(ctx, c.setSQL, b.args(id)...).Scan(&row)
		})
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		rest.SetETag(w, row)
		rest.WriteJSON(w, 200, row)
	}
}

func (m *module) collectionDelete(c collection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := rest.IDOf(r)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		if err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 204, nil), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := c.res.GetRow(ctx, tx, id); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "SELECT "+c.deleteFn+"($1::uuid)", id)
			return err
		}); err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		w.WriteHeader(204)
	}
}

func (m *module) membersList(c collection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := rest.IDOf(r)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		var rows []json.RawMessage
		err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := c.res.GetRow(ctx, tx, id); err != nil {
				return err
			}
			rows, err = rest.Rows(ctx, tx, "SELECT row_to_json(t) FROM "+c.membersFn+"($1::uuid) t", id)
			return err
		})
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		body, _ := json.Marshal(rows)
		rest.WriteJSON(w, 200, body)
	}
}

func (m *module) membersAdd(c collection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := rest.IDOf(r)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		var b struct {
			ID string `json:"id"`
		}
		raw, err := rest.ReadBody(r, &b)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		if !rest.IsUUID(b.ID) {
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "id of the user is required"))
			return
		}
		if err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 204, raw), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, c.addSQL, id, b.ID)
			return err
		}); err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		w.WriteHeader(204)
	}
}

func (m *module) membersDelete(c collection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := rest.IDOf(r)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		uid := r.PathValue("uid")
		if !rest.IsUUID(uid) {
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "id must be a UUID"))
			return
		}
		if err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 204, nil), func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, c.delSQL, id, uid)
			return err
		}); err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		w.WriteHeader(204)
	}
}

// areaAction is POST /areas/{id}/actions/{action}: delete-safely →
// {deleted: bool} (api.safely_delete_area). The set is closed.
func (m *module) areaAction(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	if r.PathValue("action") != "delete-safely" {
		rest.Fail(w, r, m.log, problem.New(404, "not-found", "Not found", "no such action"))
		return
	}
	var deleted bool
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := areas.res.GetRow(ctx, tx, id); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT api.safely_delete_area($1::uuid)", id).Scan(&deleted)
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	body, _ := json.Marshal(map[string]bool{"deleted": deleted})
	rest.WriteJSON(w, 200, body)
}

// areasClear is POST /areas/actions/clear — api.clear_area(): the number of
// areas removed (v1 /admin/area/clear).
func (m *module) areasClear(w http.ResponseWriter, r *http.Request) {
	var n int
	err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT api.clear_area()").Scan(&n)
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	body, _ := json.Marshal(map[string]int{"cleared": n})
	rest.WriteJSON(w, 200, body)
}
