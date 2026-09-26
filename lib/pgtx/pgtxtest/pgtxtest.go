// Package pgtxtest is what an integration test of a GoAPI package needs on
// the daemon road: a session with a token the database issued (daemon.begin
// verifies the bearer again, so a token signed by the test's own key is not
// enough), and a Doer that hands that token to the request while the host
// under test keeps verifying its own test-signed one.
//
// The session is minted on the test's own administrator connection, the way
// AuthServer signs a user in: api.login, then the one-time authorization code
// exchanged for an access token of the system audience. Never used by a
// service — only by tests, on a DSN read from their environment.
package pgtxtest

import (
	"context"
	"fmt"
	"sync"

	"github.com/apostoldevel/go-platform/lib/pgtx"
	"github.com/jackc/pgx/v5"
)

// Login signs user in on conn (a connection whose role may call the kernel's
// OAuth 2.0 functions — the administrator's) for audience, the client id a
// front-end signs in under, and returns the session code and an access token
// the database issued for it, valid for an hour: CreateOAuth2 → Login → the
// one-time authorization code → ExchangeToken. The token belongs to the
// audience, so daemon.begin's TokenValidation accepts it. The caller signs
// the session out (api.signout) when done.
func Login(ctx context.Context, conn *pgx.Conn, audience, user, password, agent string) (session, token string, err error) {
	var oauth2 *int64
	if err := conn.QueryRow(ctx, "SELECT kernel.createoauth2(kernel.getaudience($1), current_database())", audience).Scan(&oauth2); err != nil {
		return "", "", fmt.Errorf("pgtxtest: oauth2 of %s: %w", audience, err)
	}
	if oauth2 == nil {
		return "", "", fmt.Errorf("pgtxtest: no audience %q", audience)
	}
	var s *string
	if err := conn.QueryRow(ctx, "SELECT kernel.login($1, $2, $3, $4, '127.0.0.1')", *oauth2, user, password, agent).Scan(&s); err != nil {
		return "", "", fmt.Errorf("pgtxtest: login %s: %w", user, err)
	}
	if s == nil {
		return "", "", fmt.Errorf("pgtxtest: login %s refused", user)
	}
	var code *string
	if err := conn.QueryRow(ctx, "SELECT kernel.oauth2_current_code($1)", *s).Scan(&code); err != nil || code == nil {
		return "", "", fmt.Errorf("pgtxtest: authorization code of %s: %v", user, err)
	}
	var tok *string
	var answer []byte
	if err := conn.QueryRow(ctx, "SELECT t->>'access_token', t::text FROM (SELECT kernel.exchangetoken(kernel.getaudience($1), $2, interval '1 hour', 'C')::jsonb t) x", audience, *code).Scan(&tok, &answer); err != nil {
		return "", "", fmt.Errorf("pgtxtest: exchange the code of %s: %w", user, err)
	}
	if tok == nil {
		return "", "", fmt.Errorf("pgtxtest: exchange the code of %s: %s", user, answer)
	}
	return *s, *tok, nil
}

// Doer is the request runner a package takes (rest.Doer's method set).
type Doer interface {
	Do(ctx context.Context, s pgtx.Session, req *pgtx.Request, fn func(context.Context, pgx.Tx) error) error
}

// Tokens remembers the token the database issued for each session code.
type Tokens struct{ m sync.Map }

// Add records the token of a session.
func (t *Tokens) Add(session, token string) { t.m.Store(session, token) }

// Token is the token recorded for a session, "" when none.
func (t *Tokens) Token(session string) string {
	v, _ := t.m.Load(session)
	s, _ := v.(string)
	return s
}

// Doer wraps d: every request of a recorded session goes to the database
// with the token issued for it, in place of the one the host verified.
// A session with no recorded token keeps its own — and daemon.begin
// refuses it, as it would refuse a stranger.
func (t *Tokens) Doer(d Doer) Doer { return tokenDoer{d: d, t: t} }

type tokenDoer struct {
	d Doer
	t *Tokens
}

func (td tokenDoer) Do(ctx context.Context, s pgtx.Session, req *pgtx.Request, fn func(context.Context, pgx.Tx) error) error {
	if tok := td.t.Token(s.Code); tok != "" {
		s.Token = tok
	}
	return td.d.Do(ctx, s, req, fn)
}
