package admin

import (
	"context"
	"encoding/json"
	"net/http"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
	"github.com/jackc/pgx/v5"
)

// collection is one of the admin module's "named things users belong to":
// groups, areas, interfaces — api.set_<x>/delete_<x> and the members
// api.<x>_member / <x>_member_add / <x>_member_delete. The SQL of add and
// delete name their parameters differently per collection (pGroupId,
// pArea, pInterface …): each call is spelled out as a function and the key
// of the collection's id in it; the member is always "member".
type collection struct {
	rest.Writable
	membersFn, membersKey string // api.group_member(pGroupId)
	addFn, delFn, key     string // api.group_member_add(pGroup, pMember)
}

var groups = collection{
	Writable: rest.Writable{
		Resource: rest.Resource{Prefix: "/api/v2/groups", GetFn: "api.get_group", ListFn: "api.list_group", CountFn: "api.count_group"},
		SetFn:    "api.set_group",
		NewBody:  func() rest.Body { return &groupBody{} },
		DeleteFn: "api.delete_group",
	},
	membersFn: "api.group_member", membersKey: "groupid",
	addFn: "api.group_member_add", delFn: "api.group_member_delete", key: "group",
}

var areas = collection{
	Writable: rest.Writable{
		Resource: rest.Resource{Prefix: "/api/v2/areas", GetFn: "api.get_area", ListFn: "api.list_area", CountFn: "api.count_area"},
		SetFn:    "api.set_area",
		NewBody:  func() rest.Body { return &areaBody{} },
		DeleteFn: "api.delete_area",
	},
	membersFn: "api.area_member", membersKey: "areaid",
	addFn: "api.area_member_add", delFn: "api.area_member_delete", key: "area",
}

var interfaces = collection{
	Writable: rest.Writable{
		Resource: rest.Resource{Prefix: "/api/v2/interfaces", GetFn: "api.get_interface", ListFn: "api.list_interface", CountFn: "api.count_interface"},
		SetFn:    "api.set_interface",
		NewBody:  func() rest.Body { return &interfaceBody{} },
		DeleteFn: "api.delete_interface",
	},
	membersFn: "api.interface_member", membersKey: "interfaceid",
	addFn: "api.interface_member_add", delFn: "api.interface_member_delete", key: "interface",
}

// areaTypes is api.list_area_type (v1 /admin/area/type); sessions is
// api.list_session/count_session (v1 /admin/session/*) — read-only: a session
// code is a credential and is never addressed in a path.
var (
	areaTypes = rest.Resource{Prefix: "/api/v2/area-types"}
	// locales is api.list_locale (v1 /locale of rest.api): the languages of the texts
	locales  = rest.Resource{Prefix: "/api/v2/locales"}
	sessions = rest.Resource{Prefix: "/api/v2/sessions", ListFn: "api.list_session", CountFn: "api.count_session"}
)

type groupBody struct {
	Username    *string `json:"username"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (b *groupBody) Validate(create bool) error { return rest.Required("username", b.Username, create) }
func (b *groupBody) Args(id any) pgtx.Args {
	return pgtx.Args{"id": id, "username": b.Username, "name": b.Name, "description": b.Description}
}

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

func (b *areaBody) Validate(create bool) error { return rest.Required("code", b.Code, create) }
func (b *areaBody) Args(id any) pgtx.Args {
	return pgtx.Args{"id": id, "parent": b.Parent, "type": b.Type, "scope": b.Scope, "code": b.Code, "name": b.Name, "description": b.Description, "sequence": b.Sequence, "validfromdate": b.ValidFromDate, "validtodate": b.ValidToDate}
}

type interfaceBody struct {
	Code        *string `json:"code"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

func (b *interfaceBody) Validate(create bool) error { return rest.Required("code", b.Code, create) }
func (b *interfaceBody) Args(id any) pgtx.Args {
	return pgtx.Args{"id": id, "code": b.Code, "name": b.Name, "description": b.Description}
}

func (m *module) collectionRoutes(mux *http.ServeMux) {
	for _, c := range []collection{groups, areas, interfaces} {
		p := c.Prefix
		c.Routes(mux, m.cfg.Doer, m.idem, m.log)
		mux.HandleFunc("GET "+p+"/{id}/members", m.membersList(c))
		mux.HandleFunc("POST "+p+"/{id}/members", m.membersAdd(c))
		mux.HandleFunc("DELETE "+p+"/{id}/members/{uid}", m.membersDelete(c))
	}
	mux.HandleFunc("POST "+areas.Prefix+"/{id}/actions/{action}", m.areaAction)
	mux.HandleFunc("POST "+areas.Prefix+"/actions/clear", m.areasClear)
	mux.HandleFunc("GET "+areaTypes.Prefix, rest.CallRowsHandler(m.cfg.Doer, m.log, "list_area_type", pgtx.Args{"limit": 0}))
	mux.HandleFunc("GET "+locales.Prefix, rest.CallRowsHandler(m.cfg.Doer, m.log, "list_locale", pgtx.Args{"orderby": json.RawMessage(`["code ASC"]`), "limit": 0}))
	mux.HandleFunc("GET "+sessions.Prefix, sessions.List(m.cfg.Doer, m.log))
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
			if _, err := c.GetRow(ctx, tx, id); err != nil {
				return err
			}
			rows, err = pgtx.Call(ctx, tx, c.membersFn, pgtx.Args{c.membersKey: id})
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
			_, err := pgtx.Call(ctx, tx, c.addFn, pgtx.Args{c.key: id, "member": b.ID})
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
			_, err := pgtx.Call(ctx, tx, c.delFn, pgtx.Args{c.key: id, "member": uid})
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
		if _, err := areas.GetRow(ctx, tx, id); err != nil {
			return err
		}
		return pgtx.CallScalar(ctx, tx, "safely_delete_area", pgtx.Args{"id": id}, &deleted)
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
		return pgtx.CallScalar(ctx, tx, "clear_area", nil, &n)
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	body, _ := json.Marshal(map[string]int{"cleared": n})
	rest.WriteJSON(w, 200, body)
}
