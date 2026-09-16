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

// Body is the writable part of a resource's row: the parameters of its
// api.set_<x>, in order, after the id. Validate(create): on create a
// required field must be present; on PATCH it may be absent (kept) but not
// emptied.
type Body interface {
	Validate(create bool) error
	Args(id any) []any
}

// Required is the one rule both ways: present and empty is always wrong,
// absent is wrong only on create.
func Required(name string, v *string, create bool) error {
	if (v == nil && create) || (v != nil && *v == "") {
		return problem.New(400, "validation", "Bad request", name+" is required")
	}
	return nil
}

// Field names one *string of a body for RequiredAll.
type Field struct {
	Name string
	V    *string
}

// Req names a field for RequiredAll.
func Req(name string, v *string) Field { return Field{Name: name, V: v} }

// RequiredAll is Required over several fields, in order.
func RequiredAll(create bool, fields ...Field) error {
	for _, f := range fields {
		if err := Required(f.Name, f.V, create); err != nil {
			return err
		}
	}
	return nil
}

// LocationOf is the URL of a created row: the prefix and the row's id in the
// type the resource has — a UUID string, or a positive bigint for journals.
// An id of another shape gives no Location.
func LocationOf(res Resource, row json.RawMessage) string {
	if res.IntID {
		var r struct {
			ID *int64 `json:"id"`
		}
		if json.Unmarshal(row, &r) != nil || r.ID == nil || *r.ID < 1 {
			return ""
		}
		return res.Prefix + "/" + strconv.FormatInt(*r.ID, 10)
	}
	var r struct {
		ID *string `json:"id"`
	}
	if json.Unmarshal(row, &r) != nil || r.ID == nil || !IsUUID(*r.ID) {
		return ""
	}
	return res.Prefix + "/" + *r.ID
}

// Writable is a Resource with api.set_<x> and api.delete_<x> — the shape
// db-platform gives every entity, reference and admin collection: POST
// creates (id NULL), PATCH updates (id set), DELETE deletes.
type Writable struct {
	Resource
	SetSQL   string      // "SELECT row_to_json(t) FROM api.set_x($1::uuid, $2, …) t"
	NewBody  func() Body // a fresh body to decode into
	DeleteFn string      // "api.delete_x"
	// Redact, when set, rewrites the body before api.log_request (passwords).
	Redact func([]byte) []byte
	// Defaults, when set, fills what the database will not accept as NULL on create.
	Defaults func(Body)
}

// Routes registers the verbs of the resource on the mux: five, or four when
// the database has no api.delete_<x> (DeleteFn empty) — then DELETE is 405.
func (wr Writable) Routes(mux *http.ServeMux, d Doer, idem *Idempotency, log *slog.Logger) {
	p := wr.Prefix
	mux.HandleFunc("GET "+p, wr.List(d, log))
	mux.HandleFunc("GET "+p+"/{id}", wr.Get(d, log))
	mux.HandleFunc("POST "+p, wr.Create(d, idem, log))
	mux.HandleFunc("PATCH "+p+"/{id}", wr.Update(d, log))
	if wr.DeleteFn != "" {
		mux.HandleFunc("DELETE "+p+"/{id}", wr.Delete(d, log))
	}
}

func (wr Writable) logged(raw []byte) []byte {
	if wr.Redact != nil {
		return wr.Redact(raw)
	}
	return raw
}

// Create is POST <prefix>: the body → api.set_<x>(NULL, …) → 201 with
// Location and ETag; Idempotency-Key replays the answer for the same body.
func (wr Writable) Create(d Doer, idem *Idempotency, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b := wr.NewBody()
		raw, err := ReadBody(r, b)
		if err == nil && len(raw) == 0 {
			err = problem.New(400, "validation", "Bad request", "a JSON body is required")
		}
		if err == nil {
			err = b.Validate(true)
		}
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		if wr.Defaults != nil {
			wr.Defaults(b)
		}
		Once(w, r, idem, platform.SessionOf(r).Code, raw, log, func(w http.ResponseWriter) {
			var row json.RawMessage
			err := d.Do(r.Context(), platform.SessionOf(r), ReqOf(r, 201, wr.logged(raw)), func(ctx context.Context, tx pgx.Tx) error {
				return tx.QueryRow(ctx, wr.SetSQL, b.Args(nil)...).Scan(&row)
			})
			if err != nil {
				Fail(w, r, log, err)
				return
			}
			if loc := LocationOf(wr.Resource, row); loc != "" {
				w.Header().Set("Location", loc)
			}
			SetETag(w, row)
			WriteJSON(w, 201, row)
		})
	}
}

// Update is PATCH <prefix>/{id}: id, then If-Match (428 needs no body), then
// the body; inside the transaction the current row is read, If-Match compared
// (412), api.set_<x>(id, …) run, the new row answered with its ETag.
func (wr Writable) Update(d Doer, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := wr.id(r)
		if err == nil {
			err = Precondition(r)
		}
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		b := wr.NewBody()
		raw, err := ReadBody(r, b)
		if err == nil {
			err = b.Validate(false)
		}
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		var row json.RawMessage
		err = d.Do(r.Context(), platform.SessionOf(r), ReqOf(r, 200, wr.logged(raw)), func(ctx context.Context, tx pgx.Tx) error {
			current, err := wr.GetRow(ctx, tx, id)
			if err != nil {
				return err
			}
			if err := IfMatch(r, current); err != nil {
				return err
			}
			return tx.QueryRow(ctx, wr.SetSQL, b.Args(id)...).Scan(&row)
		})
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		SetETag(w, row)
		WriteJSON(w, 200, row)
	}
}

// Delete is DELETE <prefix>/{id}: the row must be visible (404 otherwise),
// then api.delete_<x>(id) → 204.
func (wr Writable) Delete(d Doer, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := wr.id(r)
		if err != nil {
			Fail(w, r, log, err)
			return
		}
		cast := "$1::uuid"
		if wr.IntID {
			cast = "$1::bigint"
		}
		if err := d.Do(r.Context(), platform.SessionOf(r), ReqOf(r, 204, nil), func(ctx context.Context, tx pgx.Tx) error {
			if _, err := wr.GetRow(ctx, tx, id); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "SELECT "+wr.DeleteFn+"("+cast+")", id)
			return err
		}); err != nil {
			Fail(w, r, log, err)
			return
		}
		w.WriteHeader(204)
	}
}
