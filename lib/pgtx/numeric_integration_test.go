//go:build integration

package pgtx_test

import (
	"context"
	"os"
	"testing"

	"github.com/apostoldevel/go-platform/lib/numeric"
	"github.com/apostoldevel/go-platform/lib/pgtx"
)

// A numeric.Value of 20+ significant digits reaches the database exact on
// both ways it travels: as text of a bind parameter (the direct road) and as
// a JSON number of the argument object (the daemon road, daemon.call).
func TestIntegration_NumericValueRoundTripsExactly(t *testing.T) {
	dsn := os.Getenv("GO_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("GO_TEST_PG_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgtx.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	const digits = "12345678901234567890.123456789"
	v := numeric.Value(digits)

	var viaText string
	if err := pool.QueryRow(ctx, "SELECT ($1::numeric)::text", v).Scan(&viaText); err != nil || viaText != digits {
		t.Fatalf("bind parameter: %q (%v), want %s", viaText, err, digits)
	}
	body, err := pgtx.Args{"amount": v}.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var viaJSON string
	if err := pool.QueryRow(ctx, "SELECT ($1::jsonb->>'amount')::numeric::text", string(body)).Scan(&viaJSON); err != nil || viaJSON != digits {
		t.Fatalf("JSON argument: %q (%v), want %s", viaJSON, err, digits)
	}
}
