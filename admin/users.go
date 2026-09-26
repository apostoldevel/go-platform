package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/apostoldevel/go-platform/lib/rest"
	"github.com/jackc/pgx/v5"
)

// users is /api/v2/users over api.user — the v1 routes /user/* and
// /admin/user/* (api.get_user, api.list_user, api.count_user, api.set_user,
// api.delete_user, api.set_user_profile, api.user_lock/unlock,
// api.change_password, api.get/set_user_iptable, api.member_*, api.user_member).
var users = rest.Writable{
	Resource: rest.Resource{Prefix: "/api/v2/users", GetFn: "api.get_user", ListFn: "api.list_user", CountFn: "api.count_user"},
	SetFn:    "api.set_user",
	NewBody:  func() rest.Body { return &userBody{} },
	DeleteFn: "api.delete_user",
	Redact:   redact, // absent flags default in api.add_user (db-platform ≥ 1.2.21)
}

func (m *module) userRoutes(mux *http.ServeMux) {
	p := users.Prefix
	users.Routes(mux, m.cfg.Doer, m.idem, m.log)
	mux.HandleFunc("PATCH "+p+"/{id}/profile", m.userProfile)
	mux.HandleFunc("POST "+p+"/{id}/actions/{action}", m.userAction)
	mux.HandleFunc("GET "+p+"/{id}/iptable", m.userIptableGet)
	mux.HandleFunc("PUT "+p+"/{id}/iptable", m.userIptablePut)
	mux.HandleFunc("GET "+p+"/{id}/memberships", m.userMemberships)
	for _, ms := range memberships {
		mux.HandleFunc("GET "+p+"/{id}/"+ms.path, m.membershipList(ms))
		mux.HandleFunc("POST "+p+"/{id}/"+ms.path, m.membershipAdd(ms))
		mux.HandleFunc("DELETE "+p+"/{id}/"+ms.path+"/{gid}", m.membershipDelete(ms))
	}
}

// userBody is the writable part of api.user — the parameters of api.set_user.
type userBody struct {
	Username          *string `json:"username"`
	Password          *string `json:"password"`
	Name              *string `json:"name"`
	Phone             *string `json:"phone"`
	Email             *string `json:"email"`
	Description       *string `json:"description"`
	PasswordChange    *bool   `json:"passwordchange"`
	PasswordNotChange *bool   `json:"passwordnotchange"`
}

func (b *userBody) Validate(create bool) error { return rest.Required("username", b.Username, create) }
func (b *userBody) Args(id any) pgtx.Args {
	return pgtx.Args{"id": id, "username": b.Username, "password": b.Password, "name": b.Name, "phone": b.Phone, "email": b.Email, "description": b.Description, "passwordchange": b.PasswordChange, "passwordnotchange": b.PasswordNotChange}
}

// redact keeps passwords out of api.log_request, as AddApiLog does for v1.
func redact(raw []byte) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	changed := false
	for _, k := range []string{"password", "old", "new"} {
		if _, ok := m[k]; ok {
			m[k] = json.RawMessage(`"***"`)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	out, _ := json.Marshal(m)
	return out
}

// patch runs a PATCH of the user: If-Match against the current row, the
// update, the new row back with its ETag.
// patchHead is the order every PATCH of /api/v2 decides in: id, then
// If-Match (428 needs no body), then the body.
func patchHead(r *http.Request) (string, error) {
	id, err := rest.IDOf(r)
	if err != nil {
		return "", err
	}
	return id, rest.Precondition(r)
}

func (m *module) patch(w http.ResponseWriter, r *http.Request, id string, raw []byte, update func(ctx context.Context, tx pgx.Tx, id string) error) {
	var row json.RawMessage
	err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, redact(raw)), func(ctx context.Context, tx pgx.Tx) error {
		current, err := users.GetRow(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := rest.IfMatch(r, current); err != nil {
			return err
		}
		if err := update(ctx, tx, id); err != nil {
			return err
		}
		row, err = users.GetRow(ctx, tx, id)
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	rest.SetETag(w, row)
	rest.WriteJSON(w, 200, row)
}

// profileBody is the parameters of api.set_user_profile.
type profileBody struct {
	FamilyName     *string `json:"family_name"`
	GivenName      *string `json:"given_name"`
	PatronymicName *string `json:"patronymic_name"`
	Locale         *string `json:"locale"`
	Area           *string `json:"area"`
	Interface      *string `json:"interface"`
	Picture        *string `json:"picture"`
}

func (m *module) userProfile(w http.ResponseWriter, r *http.Request) {
	id, err := patchHead(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	var b profileBody
	raw, err := rest.ReadBody(r, &b)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	m.patch(w, r, id, raw, func(ctx context.Context, tx pgx.Tx, id string) error {
		_, err := pgtx.Call(ctx, tx, "set_user_profile", pgtx.Args{"userid": id, "familyname": b.FamilyName, "givenname": b.GivenName, "patronymicname": b.PatronymicName, "locale": b.Locale, "area": b.Area, "interface": b.Interface, "picture": b.Picture})
		return err
	})
}

// userAction is POST /users/{id}/actions/{action}: lock, unlock — the row
// back; change-password {old, new} — 204. Unknown actions are 404: the set
// is closed, unlike an entity's automaton.
func (m *module) userAction(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	var fn string
	switch r.PathValue("action") {
	case "lock":
		fn = "api.user_lock"
	case "unlock":
		fn = "api.user_unlock"
	case "change-password":
		m.changePassword(w, r, id)
		return
	default:
		rest.Fail(w, r, m.log, problem.New(404, "not-found", "Not found", "no such action"))
		return
	}
	var row json.RawMessage
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := users.GetRow(ctx, tx, id); err != nil {
			return err
		}
		if _, err := pgtx.Call(ctx, tx, fn, pgtx.Args{"id": id}); err != nil {
			return err
		}
		row, err = users.GetRow(ctx, tx, id)
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	rest.SetETag(w, row)
	rest.WriteJSON(w, 200, row)
}

func (m *module) changePassword(w http.ResponseWriter, r *http.Request, id string) {
	var b struct {
		Old *string `json:"old"`
		New *string `json:"new"`
	}
	raw, err := rest.ReadBody(r, &b)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	if b.Old == nil || *b.Old == "" || b.New == nil || *b.New == "" {
		rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "old and new passwords are required"))
		return
	}
	if err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 204, redact(raw)), func(ctx context.Context, tx pgx.Tx) error {
		_, err := pgtx.Call(ctx, tx, "change_password", pgtx.Args{"id": id, "oldpass": *b.Old, "newpass": *b.New})
		return err
	}); err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	w.WriteHeader(204)
}

// ── iptable ────────────────────────────────────────────────────────────

// iptable is the pair api.get_user_iptable(id, 'A') / (id, 'D'): the
// comma-separated text of v1 as two arrays. Entries are in the database's
// dialect (str_to_inet): "192.168.1.1", "10.0.0.1-10.0.0.5", "192.168.*.*" —
// not CIDR; the package passes them through and the database refuses the rest.
type iptable struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (m *module) userIptableGet(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	var t iptable
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := users.GetRow(ctx, tx, id); err != nil {
			return err
		}
		for _, kind := range []struct {
			code string
			into *[]string
		}{{"A", &t.Allow}, {"D", &t.Deny}} {
			var list *string
			row, err := pgtx.CallRow(ctx, tx, "get_user_iptable", pgtx.Args{"id": id, "type": kind.code})
			if err != nil && !errors.Is(err, pgtx.ErrNoRow) {
				return err
			}
			if row != nil {
				var t struct {
					Iptable *string `json:"iptable"`
				}
				if err := json.Unmarshal(row, &t); err != nil {
					return err
				}
				list = t.Iptable
			}
			*kind.into = []string{}
			if list != nil {
				*kind.into = splitList(*list)
			}
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	body, _ := json.Marshal(t)
	rest.WriteJSON(w, 200, body)
}

func (m *module) userIptablePut(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	var t iptable
	raw, err := rest.ReadBody(r, &t)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, raw), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := users.GetRow(ctx, tx, id); err != nil {
			return err
		}
		for _, kind := range []struct {
			code string
			list []string
		}{{"A", t.Allow}, {"D", t.Deny}} {
			var text *string
			if len(kind.list) > 0 {
				s := strings.Join(kind.list, ",")
				text = &s
			}
			if _, err := pgtx.Call(ctx, tx, "set_user_iptable", pgtx.Args{"id": id, "type": kind.code, "iptable": text}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	if t.Allow == nil {
		t.Allow = []string{}
	}
	if t.Deny == nil {
		t.Deny = []string{}
	}
	body, _ := json.Marshal(t)
	rest.WriteJSON(w, 200, body)
}

// ── memberships ────────────────────────────────────────────────────────

// membership is one of the three "user is a member of" relations of the admin
// module: api.member_<x>(pUserId) lists, api.member_<x>_add/_delete(pMember,
// p<X>) change — key is the name of that second parameter.
type membership struct {
	path string // groups | areas | interfaces
	fn   string // api.member_group | api.member_area | api.member_interface
	key  string // group | area | interface
}

var memberships = []membership{
	{"groups", "api.member_group", "group"},
	{"areas", "api.member_area", "area"},
	{"interfaces", "api.member_interface", "interface"},
}

func (m *module) membershipList(ms membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := rest.IDOf(r)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		var rows []json.RawMessage
		err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := users.GetRow(ctx, tx, id); err != nil {
				return err
			}
			rows, err = pgtx.Call(ctx, tx, ms.fn, pgtx.Args{"userid": id})
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

func (m *module) membershipAdd(ms membership) http.HandlerFunc {
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
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "id of the "+strings.TrimSuffix(ms.path, "s")+" is required"))
			return
		}
		if err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 204, raw), func(ctx context.Context, tx pgx.Tx) error {
			_, err := pgtx.Call(ctx, tx, ms.fn+"_add", pgtx.Args{"member": id, ms.key: b.ID})
			return err
		}); err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		w.WriteHeader(204)
	}
}

func (m *module) membershipDelete(ms membership) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := rest.IDOf(r)
		if err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		gid := r.PathValue("gid")
		if !rest.IsUUID(gid) {
			rest.Fail(w, r, m.log, problem.New(400, "validation", "Bad request", "id must be a UUID"))
			return
		}
		if err := m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 204, nil), func(ctx context.Context, tx pgx.Tx) error {
			_, err := pgtx.Call(ctx, tx, ms.fn+"_delete", pgtx.Args{"member": id, ms.key: gid})
			return err
		}); err != nil {
			rest.Fail(w, r, m.log, err)
			return
		}
		w.WriteHeader(204)
	}
}

// userMemberships is GET /users/{id}/memberships — api.user_member: the
// groups, areas and interfaces of the user in one list (v1 /admin/user/member).
func (m *module) userMemberships(w http.ResponseWriter, r *http.Request) {
	id, err := rest.IDOf(r)
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	var rows []json.RawMessage
	err = m.cfg.Doer.Do(r.Context(), platform.SessionOf(r), rest.ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := users.GetRow(ctx, tx, id); err != nil {
			return err
		}
		rows, err = pgtx.Call(ctx, tx, "user_member", pgtx.Args{"userid": id})
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	body, _ := json.Marshal(rows)
	rest.WriteJSON(w, 200, body)
}
