//go:build integration

package pgtx_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/jackc/pgx/v5"
)

// Environment: GO_TEST_PG_DSN — the module's connection as
// apibot; GO_TEST_ADMIN_DSN — used ONLY to mint a live session with
// api.login(admin, <password of the DSN>), the way AuthServer would. Both are
// read from the environment, never from code.
func env(t *testing.T) (pool string, mint func(t *testing.T) string) {
	t.Helper()
	dsn, admin := os.Getenv("GO_TEST_PG_DSN"), os.Getenv("GO_TEST_ADMIN_DSN")
	if dsn == "" || admin == "" {
		t.Skip("GO_TEST_PG_DSN / GO_TEST_ADMIN_DSN not set")
	}
	cfg, err := pgx.ParseConfig(admin)
	if err != nil {
		t.Fatal(err)
	}
	mint = func(t *testing.T) string {
		t.Helper()
		conn, err := pgx.Connect(context.Background(), admin)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(context.Background())
		var session string
		if err := conn.QueryRow(context.Background(), "SELECT session FROM api.login($1, $2, 'go-platform-test', '127.0.0.1')", cfg.User, cfg.Password).Scan(&session); err != nil {
			t.Fatalf("mint session: %v", err)
		}
		t.Cleanup(func() {
			c, err := pgx.Connect(context.Background(), admin)
			if err == nil {
				_, _ = c.Exec(context.Background(), "SELECT api.signout($1)", session)
				c.Close(context.Background())
			}
		})
		return session
	}
	return dsn, mint
}

func runner(t *testing.T, dsn string) *pgtx.Runner {
	t.Helper()
	pool, err := pgtx.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &pgtx.Runner{Pool: pool}
}

func TestIntegration_AuthorizedRequestCommits(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	var n int64
	err := r.Do(context.Background(), pgtx.Session{Code: mint(t), Agent: "go-test", Host: "127.0.0.1"}, nil, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT api.count_client()").Scan(&n)
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("count_client = %d", n)
	}
}

func TestIntegration_BadSessionIs401(t *testing.T) {
	dsn, _ := env(t)
	r := runner(t, dsn)
	err := r.Do(context.Background(), pgtx.Session{Code: "0000000000000000000000000000000000000000"}, nil, func(ctx context.Context, tx pgx.Tx) error {
		t.Fatal("handler must not run")
		return nil
	})
	var p *problem.Problem
	if !errors.As(err, &p) || p.Status != 401 {
		t.Fatalf("got %v", err)
	}
}

func TestIntegration_CatalogueErrorBecomesProblem(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	err := r.Do(context.Background(), pgtx.Session{Code: mint(t)}, nil, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "SELECT api.execute_object_action('00000000-0000-4000-b000-00000000dead'::uuid, 'enable')")
		return err
	})
	var p *problem.Problem
	if !errors.As(err, &p) {
		t.Fatalf("got %v", err)
	}
	if p.Code == nil || p.Status < 400 || p.Status >= 500 || p.Title == "" || p.Detail == "" {
		t.Fatalf("problem %+v", p)
	}
	t.Logf("catalogue: %s %d %q / %q", *p.Code, p.Status, p.Title, p.Detail)
}

func TestIntegration_TwoSessionsDoNotLeakIntoEachOther(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	a, b := mint(t), mint(t)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 20; i++ {
		for _, code := range []string{a, b} {
			wg.Add(1)
			go func(code string) {
				defer wg.Done()
				errs <- r.Do(context.Background(), pgtx.Session{Code: code}, nil, func(ctx context.Context, tx pgx.Tx) error {
					time.Sleep(5 * time.Millisecond)
					var seen string
					if err := tx.QueryRow(ctx, "SELECT current_setting('current.session', true)").Scan(&seen); err != nil {
						return err
					}
					if seen != code {
						return errors.New("transaction sees another session: " + seen[:8] + " ≠ " + code[:8])
					}
					return nil
				})
			}(code)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestIntegration_ConnectionIsResetAfterRelease(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	if err := r.Do(context.Background(), pgtx.Session{Code: mint(t)}, nil, func(ctx context.Context, tx pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	// every pooled connection must come back without a session
	for i := 0; i < 5; i++ {
		var seen *string
		if err := r.Pool.QueryRow(context.Background(), "SELECT NULLIF(current_setting('current.session', true), '')").Scan(&seen); err != nil {
			t.Fatal(err)
		}
		if seen != nil {
			t.Fatalf("connection still carries session %s… after release", (*seen)[:8])
		}
	}
}

func TestIntegration_Detect(t *testing.T) {
	dsn, _ := env(t)
	r := runner(t, dsn)
	if err := r.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Logf("features: %+v", r.Features)
}

func TestIntegration_LogRequestWhenPatched(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	if err := r.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.Features.LogRequest {
		t.Skip("api.log_request not in this database (gateway patch not applied)")
	}
	req := &pgtx.Request{Method: "GET", Path: "/api/v2/clients", RequestID: "6f1c0b4a-1c9e-4c6e-9b0e-2b0a1b7e4d11"}
	if err := r.Do(context.Background(), pgtx.Session{Code: mint(t)}, req, func(ctx context.Context, tx pgx.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestIntegration_AuthorizeLocalIsolatesWhenPatched(t *testing.T) {
	dsn, mint := env(t)
	r := runner(t, dsn)
	if err := r.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.Features.AuthorizeLocal {
		t.Skip("api.authorize_local not in this database (gateway patch not applied)")
	}
	code := mint(t)
	var seen *string
	err := r.Do(context.Background(), pgtx.Session{Code: code}, nil, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT NULLIF(current_setting('current.session', true), '')").Scan(&seen)
	})
	if err != nil || seen == nil || *seen != code {
		t.Fatalf("inside tx: %v %v", err, seen)
	}
}
