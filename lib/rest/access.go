package rest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	platform "github.com/apostoldevel/go-platform"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/jackc/pgx/v5"
)

// AccessBody is one grant of the three access tables of db-platform (ACU
// on a class, AMU on a method, AOU on an object): the user or group and
// the mask; for a class also whether to descend the class tree and to
// rewrite the existing objects (the defaults of api.chmodc otherwise).
type AccessBody struct {
	UserID    string `json:"userid"`
	Mask      *int   `json:"mask"`
	Recursive *bool  `json:"recursive"`
	ObjectSet *bool  `json:"objectset"`
}

// Validate is the 400 of a grant without a user or a mask.
func (b *AccessBody) Validate() error {
	if !IsUUID(b.UserID) {
		return problem.New(400, "validation", "Bad request", "userid is required")
	}
	if b.Mask == nil || *b.Mask < 0 {
		return problem.New(400, "validation", "Bad request", "mask is required")
	}
	return nil
}

// The width of a mask: the database casts it to bit(n) and a wider number
// wraps — 64 into bit(6) is 000000, a revoke — so the bound is decided here.
const (
	MaskBits6  = 63   // api.chmodo, api.chmodm: deny/allow × s/u/d
	MaskBits10 = 1023 // api.chmodc: deny/allow × the five class bits
)

// Bound is the 400 of a mask wider than the grant takes, and of the class
// options (recursive, objectset) on a grant that has none.
func (b *AccessBody) Bound(max int, classOptions bool) error {
	if *b.Mask > max {
		return problem.New(400, "validation", "Bad request", "mask must be at most "+strconv.Itoa(max))
	}
	if !classOptions && (b.Recursive != nil || b.ObjectSet != nil) {
		return problem.New(400, "validation", "Bad request", "recursive and objectset apply to a class only")
	}
	return nil
}

// Access is the access form of a resource, the same for a class, a method
// and an object:
//
//	GET <prefix>/{id}/access                 who holds what — ListFn(id)
//	PUT <prefix>/{id}/access {userid, mask}  one grant — Set (api.chmodc / chmodm / chmodo); mask 0 revokes
//	GET <prefix>/{id}/access/decode[?userid=] the bits of one user, decoded — DecodeFn(id, user)
//
// The row must be visible first (404 through Resource.GetFn), so a
// foreign object answers as if it did not exist rather than with three
// NULLs — DecodeObjectAccess reads NULL & B'100' as NULL for a row it
// cannot see. The id is a uuid (Resource.IntID is not read: the three
// access tables key objects, classes and methods by uuid).
type Access struct {
	Resource     Resource
	ListFn       string // "api.object_access"
	DecodeFn     string // "api.decode_object_access"
	MaxMask      int    // MaskBits6 or MaskBits10 — decided before the database
	ClassOptions bool   // recursive and objectset are taken (a class), else refused
	Set          func(ctx context.Context, tx pgx.Tx, id string, b AccessBody) error
}

// Routes registers the three routes on the mux.
func (a Access) Routes(mux *http.ServeMux, d Doer, log *slog.Logger) {
	res, listFn, decodeFn, set := a.Resource, a.ListFn, a.DecodeFn, a.Set
	mux.HandleFunc("GET "+res.Prefix+"/{id}/access", func(w http.ResponseWriter, r *http.Request) {
		id, err := IDOf(r)
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		var rows []json.RawMessage
		err = d.Do(r.Context(), platform.SessionOf(r), ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := res.GetRow(ctx, tx, id); err != nil {
				return err
			}
			rows, err = Rows(ctx, tx, "SELECT row_to_json(t) FROM "+listFn+"($1::uuid) t", id)
			return err
		})
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		body, _ := json.Marshal(rows)
		WriteJSON(w, 200, body)
	})
	mux.HandleFunc("PUT "+res.Prefix+"/{id}/access", func(w http.ResponseWriter, r *http.Request) {
		id, err := IDOf(r)
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		var b AccessBody
		raw, err := ReadBody(r, &b)
		if err == nil {
			err = b.Validate()
		}
		if err == nil {
			err = b.Bound(a.MaxMask, a.ClassOptions)
		}
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		var rows []json.RawMessage
		err = d.Do(r.Context(), platform.SessionOf(r), ReqOf(r, 200, raw), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := res.GetRow(ctx, tx, id); err != nil {
				return err
			}
			if err := set(ctx, tx, id, b); err != nil {
				return err
			}
			rows, err = Rows(ctx, tx, "SELECT row_to_json(t) FROM "+listFn+"($1::uuid) t", id)
			return err
		})
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		body, _ := json.Marshal(rows)
		WriteJSON(w, 200, body)
	})
	mux.HandleFunc("GET "+res.Prefix+"/{id}/access/decode", func(w http.ResponseWriter, r *http.Request) {
		id, err := IDOf(r)
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		var user *string
		if u := r.URL.Query().Get("userid"); u != "" {
			if !IsUUID(u) {
				Fail(w, r, log, problem.New(400, "validation", "Bad request", "userid must be a UUID"))
				return
			}
			user = &u
		}
		var row json.RawMessage
		err = d.Do(r.Context(), platform.SessionOf(r), ReqOf(r, 200, nil), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := res.GetRow(ctx, tx, id); err != nil {
				return err
			}
			return tx.QueryRow(ctx, "SELECT row_to_json(t) FROM "+decodeFn+"($1::uuid, coalesce($2::uuid, api.current_userid())) t", id, user).Scan(&row)
		})
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		WriteJSON(w, 200, row)
	})
}
