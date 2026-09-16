package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	platform "github.com/apostoldevel/go-platform"
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
	SetSQL:   "SELECT row_to_json(t) FROM api.set_user($1::uuid, $2, $3, $4, $5, $6, $7, $8::boolean, $9::boolean) t",
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
func (b *userBody) Args(id any) []any {
	return []any{id, b.Username, b.Password, b.Name, b.Phone, b.Email, b.Description, b.PasswordChange, b.PasswordNotChange}
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
		_, err := tx.Exec(ctx, "SELECT api.set_user_profile($1::uuid, $2, $3, $4, $5, $6, $7, $8)", id, b.FamilyName, b.GivenName, b.PatronymicName, b.Locale, b.Area, b.Interface, b.Picture)
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
		if _, err := tx.Exec(ctx, "SELECT "+fn+"($1::uuid)", id); err != nil {
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
		_, err := tx.Exec(ctx, "SELECT api.change_password($1::uuid, $2, $3)", id, *b.Old, *b.New)
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
			if err := tx.QueryRow(ctx, "SELECT iptable FROM api.get_user_iptable($1::uuid, $2)", id, kind.code).Scan(&list); err != nil && err != pgx.ErrNoRows {
				return err
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
			if _, err := tx.Exec(ctx, "SELECT api.set_user_iptable($1::uuid, $2, $3)", id, kind.code, text); err != nil {
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
// module: api.member_<x>(userid) lists, api.member_<x>_add/_delete change.
type membership struct {
	path string // groups | areas | interfaces
	fn   string // api.member_group | api.member_area | api.member_interface
}

var memberships = []membership{
	{"groups", "api.member_group"},
	{"areas", "api.member_area"},
	{"interfaces", "api.member_interface"},
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
			rows, err = rest.Rows(ctx, tx, "SELECT row_to_json(t) FROM "+ms.fn+"($1::uuid) t", id)
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
			_, err := tx.Exec(ctx, "SELECT "+ms.fn+"_add($1::uuid, $2::uuid)", id, b.ID)
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
			_, err := tx.Exec(ctx, "SELECT "+ms.fn+"_delete($1::uuid, $2::uuid)", id, gid)
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
		rows, err = rest.Rows(ctx, tx, "SELECT row_to_json(t) FROM api.user_member($1::uuid) t", id)
		return err
	})
	if err != nil {
		rest.Fail(w, r, m.log, err)
		return
	}
	body, _ := json.Marshal(rows)
	rest.WriteJSON(w, 200, body)
}
