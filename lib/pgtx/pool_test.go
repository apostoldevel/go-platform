package pgtx

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/lib/problem"
	"github.com/jackc/pgx/v5"
)

// silentServer accepts connections and never answers: the shape of a
// database behind a pooler whose path is dropped.
func silentServer(t *testing.T) (dsn string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		var held []net.Conn
		defer func() {
			for _, c := range held {
				_ = c.Close()
			}
		}()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			held = append(held, c)
			select {
			case <-done:
				return
			default:
			}
		}
	}()
	t.Cleanup(func() { close(done); _ = l.Close() })
	return "postgres://u:p@" + l.Addr().String() + "/d?sslmode=disable"
}

func TestNewPool_DefaultsFillWhatTheDSNLeavesOut(t *testing.T) {
	pool, err := NewPool(context.Background(), "postgres://u:p@127.0.0.1:1/d")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	c := pool.Config()
	if c.MaxConns != DefaultMaxConns || c.MaxConnIdleTime != DefaultMaxConnIdleTime || c.ConnConfig.ConnectTimeout != DefaultConnectTimeout {
		t.Fatalf("max %d idle %v connect %v", c.MaxConns, c.MaxConnIdleTime, c.ConnConfig.ConnectTimeout)
	}
}

func TestNewPool_DSNWinsOverDefaults(t *testing.T) {
	pool, err := NewPool(context.Background(), "postgres://u:p@127.0.0.1:1/d?connect_timeout=2&pool_max_conns=3&pool_max_conn_idle_time=45s")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	c := pool.Config()
	if c.MaxConns != 3 || c.MaxConnIdleTime != 45*time.Second || c.ConnConfig.ConnectTimeout != 2*time.Second {
		t.Fatalf("max %d idle %v connect %v", c.MaxConns, c.MaxConnIdleTime, c.ConnConfig.ConnectTimeout)
	}
}

func TestNewPool_OptionWinsOverDSN(t *testing.T) {
	pool, err := NewPool(context.Background(), "postgres://u:p@127.0.0.1:1/d?pool_max_conns=3", WithMaxConns(7))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if n := pool.Config().MaxConns; n != 7 {
		t.Fatalf("max %d, want 7", n)
	}
	pool2, err := NewPool(context.Background(), "postgres://u:p@127.0.0.1:1/d", WithMaxConns(0))
	if err != nil {
		t.Fatal(err)
	}
	defer pool2.Close()
	if n := pool2.Config().MaxConns; n != DefaultMaxConns {
		t.Fatalf("max %d: a ceiling below 1 must be ignored", n)
	}
}

// A database that does not answer is a fast 500 "internal" within
// BeginTimeout, on both roads — not a wait for the request's own context
// (here 5 s, in service the gateway's 30 s).
func TestDo_BeginOnASilentDatabaseIsAFast500(t *testing.T) {
	pool, err := NewPool(context.Background(), silentServer(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for name, daemon := range map[string]bool{"direct": false, "daemon": true} {
		t.Run(name, func(t *testing.T) {
			r := &Runner{Pool: pool, BeginTimeout: 300 * time.Millisecond, Features: Features{Daemon: daemon}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			started := time.Now()
			err := r.Do(ctx, Session{Token: "t", Code: "c"}, &Request{Method: "GET", Path: "/x"}, func(context.Context, pgx.Tx) error {
				t.Error("handler ran without a transaction")
				return nil
			})
			took := time.Since(started)
			var p *problem.Problem
			if !errors.As(err, &p) || p.Status != 500 || p.Type == "" {
				t.Fatalf("err %v, want a 500 problem", err)
			}
			if took < 250*time.Millisecond || took > 2*time.Second {
				t.Fatalf("took %v: want the 300ms deadline, not the request's 5s", took)
			}
		})
	}
}

// The deadline is the runner's, not the pool's: a zero or negative
// BeginTimeout is the default, never "no limit".
func TestBeginTimeout_ZeroOrNegativeIsTheDefault(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second} {
		if got := (&Runner{BeginTimeout: d}).beginTimeout(); got != DefaultBeginTimeout {
			t.Errorf("%v → %v, want %v", d, got, DefaultBeginTimeout)
		}
	}
	if got := (&Runner{BeginTimeout: time.Second}).beginTimeout(); got != time.Second {
		t.Errorf("1s → %v", got)
	}
}

func TestDSNKeys_OnlyParametersAreKeys(t *testing.T) {
	for dsn, want := range map[string][]string{
		"host=h pool_max_conns = 3 connect_timeout= 2":           {"host", "pool_max_conns", "connect_timeout"},
		"host=h password='x pool_max_conns=3' dbname=d":          {"host", "password", "dbname"},
		`host=h password='it\'s pool_max_conns=3'`:               {"host", "password"},
		"postgres://u:pa%5Fpool_max_conns=9@h/d":                 {},
		"postgres://u:p@h/d?pool_max_conns=3&connect_timeout=2":  {"pool_max_conns", "connect_timeout"},
		"postgresql://u:p@h/d?sslmode=disable&xpool_max_conns=1": {"sslmode", "xpool_max_conns"},
		"host=h xpool_max_conns=1":                               {"host", "xpool_max_conns"},
	} {
		got := dsnKeys(dsn)
		if len(got) != len(want) {
			t.Errorf("%q → %v, want %v", dsn, got, want)
			continue
		}
		for _, k := range want {
			if !got[k] {
				t.Errorf("%q → %v, want %v", dsn, got, want)
			}
		}
	}
}

func TestNewPool_DSNFormsKeepTheirValues(t *testing.T) {
	pool, err := NewPool(context.Background(), "host=127.0.0.1 port=1 user=u dbname=d pool_max_conns = 3 connect_timeout = 2")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if c := pool.Config(); c.MaxConns != 3 || c.ConnConfig.ConnectTimeout != 2*time.Second {
		t.Fatalf("max %d connect %v", c.MaxConns, c.ConnConfig.ConnectTimeout)
	}
	// a name inside a value is no parameter: the default stands
	pool2, err := NewPool(context.Background(), "host=127.0.0.1 port=1 user=u dbname=d password='x pool_max_conns=3'")
	if err != nil {
		t.Fatal(err)
	}
	defer pool2.Close()
	if n := pool2.Config().MaxConns; n != DefaultMaxConns {
		t.Fatalf("max %d, want the default %d", n, DefaultMaxConns)
	}
}

func TestNewPool_EnvironmentConnectTimeoutStays(t *testing.T) {
	t.Setenv("PGCONNECT_TIMEOUT", "3")
	pool, err := NewPool(context.Background(), "postgres://u:p@127.0.0.1:1/d")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if d := pool.Config().ConnConfig.ConnectTimeout; d != 3*time.Second {
		t.Fatalf("connect %v, want the environment's 3s", d)
	}
}
