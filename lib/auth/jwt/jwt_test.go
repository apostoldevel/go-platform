package jwt

import (
	"errors"
	"testing"
	"time"
)

var now = time.Unix(1_800_000_000, 0)

func keys() Keyring {
	return Keyring{
		Audiences: map[string]Key{
			"web-example":     {Alg: "HS256", Secret: []byte("web-secret")},
			"gateway-example": {Alg: "HS512", Secret: []byte("gw-secret")},
		},
		Issuers: []string{"accounts.example"},
	}
}

func TestVerify_Good(t *testing.T) {
	tok := Sign(Claims{Iss: "accounts.example", Aud: "web-example", Sub: "c83b2f85321f95341707624546ca6ac4fa6d1115", Iat: now.Unix(), Exp: now.Add(time.Hour).Unix()}, "HS256", []byte("web-secret"))
	c, err := keys().Verify(tok, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Sub != "c83b2f85321f95341707624546ca6ac4fa6d1115" || c.Aud != "web-example" {
		t.Fatalf("%+v", c)
	}
}

func TestVerify_HS512Audience(t *testing.T) {
	tok := Sign(Claims{Iss: "accounts.example", Aud: "gateway-example", Sub: "s", Exp: now.Add(time.Hour).Unix()}, "HS512", []byte("gw-secret"))
	if _, err := keys().Verify(tok, now); err != nil {
		t.Fatal(err)
	}
}

func TestVerify_Rejects(t *testing.T) {
	good := Claims{Iss: "accounts.example", Aud: "web-example", Sub: "s", Exp: now.Add(time.Hour).Unix()}
	cases := []struct {
		name string
		tok  string
		want error
	}{
		{"wrong secret", Sign(good, "HS256", []byte("other")), ErrSignature},
		{"alg mismatch for audience", Sign(good, "HS384", []byte("web-secret")), ErrSignature},
		{"unknown audience", Sign(Claims{Iss: "accounts.example", Aud: "ios-x", Sub: "s", Exp: now.Add(time.Hour).Unix()}, "HS256", []byte("web-secret")), ErrAudience},
		{"unknown issuer", Sign(Claims{Iss: "evil", Aud: "web-example", Sub: "s", Exp: now.Add(time.Hour).Unix()}, "HS256", []byte("web-secret")), ErrIssuer},
		{"expired", Sign(Claims{Iss: "accounts.example", Aud: "web-example", Sub: "s", Exp: now.Add(-time.Second).Unix()}, "HS256", []byte("web-secret")), ErrExpired},
		{"no exp", Sign(Claims{Iss: "accounts.example", Aud: "web-example", Sub: "s"}, "HS256", []byte("web-secret")), ErrExpired},
		{"alg none", "eyJhbGciOiJub25lIn0.eyJhdWQiOiJ3ZWItZXhhbXBsZSJ9.", ErrMalformed},
		{"garbage", "garbage", ErrMalformed},
		{"empty sub", Sign(Claims{Iss: "accounts.example", Aud: "web-example", Exp: now.Add(time.Hour).Unix()}, "HS256", []byte("web-secret")), ErrMalformed},
	}
	for _, c := range cases {
		_, err := keys().Verify(c.tok, now)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestVerify_TamperedPayload(t *testing.T) {
	tok := Sign(Claims{Iss: "accounts.example", Aud: "web-example", Sub: "a", Exp: now.Add(time.Hour).Unix()}, "HS256", []byte("web-secret"))
	other := Sign(Claims{Iss: "accounts.example", Aud: "web-example", Sub: "b", Exp: now.Add(time.Hour).Unix()}, "HS256", []byte("web-secret"))
	// header.payload of `other` with signature of `tok`
	p1, p2 := split(tok), split(other)
	forged := p2[0] + "." + p2[1] + "." + p1[2]
	if _, err := keys().Verify(forged, now); !errors.Is(err, ErrSignature) {
		t.Fatalf("forged accepted: %v", err)
	}
}

func split(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
