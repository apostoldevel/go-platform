package pgtx

import (
	"errors"
	"strings"
	"testing"

	"github.com/apostoldevel/go-platform/problem"
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
	for _, err := range []error{errors.New("boom"), &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}, &pgconn.PgError{Code: "23505", Message: "duplicate key"}} {
		if _, _, kind := classify(err); kind != kindInternal {
			t.Errorf("%v → %v", err, kind)
		}
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
