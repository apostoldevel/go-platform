package pgtx

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestClassify_CatalogueRaise(t *testing.T) {
	err := &pgconn.PgError{Code: "P0001", Message: "ERR-400-032: Object [x] method not FOUND, for action: y"}
	code, detail, kind := classify(err)
	if kind != kindCatalogue || code != "ERR-400-032" || detail != "Object [x] method not FOUND, for action: y" {
		t.Fatalf("%q %q %v", code, detail, kind)
	}
}

func TestClassify_BareRaiseIs400Unknown(t *testing.T) {
	err := &pgconn.PgError{Code: "P0001", Message: "ERR-40000: bare"}
	_, detail, kind := classify(err)
	if kind != kindRaise || detail != "ERR-40000: bare" {
		t.Fatalf("%q %v", detail, kind)
	}
}

func TestClassify_ProblemPassesThrough(t *testing.T) {
	p := problem.New(412, "precondition-failed", "Precondition failed", "udate changed")
	_, _, kind := classify(p)
	if kind != kindProblem {
		t.Fatal(kind)
	}
}

func TestClassify_OtherIsInternal(t *testing.T) {
	for _, err := range []error{errors.New("boom"), &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}} {
		if _, _, kind := classify(err); kind != kindInternal {
			t.Errorf("%v → %v", err, kind)
		}
	}
}

// A constraint the database refuses is the client's data, not a failure of
// the process: not-null/check/foreign-key are 400 with the constraint's own
// text (as v1 sends it), a duplicate key is 409. The row itself (pg Detail)
// stays out of the answer.
func TestClassify_ConstraintViolationIsTheClients(t *testing.T) {
	notNull := &pgconn.PgError{Code: "23502", Message: `null value in column "name" of relation "user" violates not-null constraint`, Detail: "Failing row contains (1, secret)."}
	code, detail, kind := classify(notNull)
	if kind != kindConstraint || code != "23502" || detail != notNull.Message {
		t.Fatalf("%q %q %v", code, detail, kind)
	}
	r := &Runner{}
	var p *problem.Problem
	if err := r.explain(context.Background(), notNull); !errors.As(err, &p) || p.Status != 400 || p.Type != "urn:apostol:error:validation" || p.Detail != notNull.Message {
		t.Fatalf("%v", err)
	}
	dup := &pgconn.PgError{Code: "23505", Message: `duplicate key value violates unique constraint "user_username_key"`, Detail: "Key (username)=(x) already exists."}
	if err := r.explain(context.Background(), dup); !errors.As(err, &p) || p.Status != 409 || p.Type != "urn:apostol:error:conflict" || strings.Contains(p.Detail, "already exists") {
		t.Fatalf("%v", err)
	}
}

func TestFeatures_FromProcNames(t *testing.T) {
	f := featuresFrom([]string{"authorize", "authorize_local", "log_request"})
	if !f.AuthorizeLocal || !f.LogRequest || f.ParseMessage {
		t.Fatalf("%+v", f)
	}
	if f := featuresFrom(nil); f.AuthorizeLocal || f.LogRequest || f.ParseMessage {
		t.Fatalf("%+v", f)
	}
}

func TestAuthorizeSQL_FollowsFeatures(t *testing.T) {
	r := &Runner{}
	if got := r.authorizeSQL(); !strings.Contains(got, "api.authorize(") {
		t.Fatal(got)
	}
	r.Features.AuthorizeLocal = true
	if got := r.authorizeSQL(); !strings.Contains(got, "api.authorize_local(") {
		t.Fatal(got)
	}
}

func TestRequest_DefaultStatus(t *testing.T) {
	var req Request
	if req.status() != 200 {
		t.Fatal(req.status())
	}
	req.Status = 201
	if req.status() != 201 {
		t.Fatal(req.status())
	}
}

// A value the database could not read as its type (class 22: a bad uuid, a
// number out of range, bad base64) is the client's — 400, not 500.
func TestClassify_DataExceptionIsTheClients(t *testing.T) {
	for _, code := range []string{"22P02", "22003", "22023", "22007"} {
		_, detail, kind := classify(&pgconn.PgError{Code: code, Message: "invalid input syntax"})
		if kind != kindData || detail != "invalid input syntax" {
			t.Fatalf("%s: %q %v", code, detail, kind)
		}
	}
	r := &Runner{}
	if err := r.explain(context.Background(), &pgconn.PgError{Code: "22P02", Message: "invalid input syntax for type uuid"}); err == nil || !strings.Contains(err.Error(), "invalid input syntax") {
		t.Fatalf("%v", err)
	}
	var p *problem.Problem
	if !errors.As(r.explain(context.Background(), &pgconn.PgError{Code: "22P02", Message: "x"}), &p) || p.Status != 400 {
		t.Fatalf("%v", p)
	}
}
