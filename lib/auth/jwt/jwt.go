// Package jwt verifies the platform's access tokens locally (contract K7):
// HMAC signature with the secret of the audience named in `aud`, `exp`, and
// an allowed `iss`. The platform signs with sign(payload, secret, alg) —
// kernel/jwt.sql — HS256/HS384/HS512 only; anything else is malformed here.
package jwt

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"hash"
	"strings"
	"time"
)

var (
	ErrMalformed = errors.New("jwt: malformed token")
	ErrSignature = errors.New("jwt: bad signature")
	ErrAudience  = errors.New("jwt: unknown audience")
	ErrIssuer    = errors.New("jwt: issuer not allowed")
	ErrExpired   = errors.New("jwt: expired")
)

// Claims are the platform claims (CreateToken, admin/routine.sql): `sub` is the
// session code.
type Claims struct {
	Iss string `json:"iss"`
	Aud string `json:"aud"`
	Sub string `json:"sub"`
	Iat int64  `json:"iat,omitempty"`
	Exp int64  `json:"exp,omitempty"`
}

// Key is one audience's signing secret and the algorithm it signs with.
type Key struct {
	Alg    string // HS256 | HS384 | HS512
	Secret []byte
}

// Keyring maps audience (client_id) → key, plus the issuers accepted.
type Keyring struct {
	Audiences map[string]Key
	Issuers   []string
}

// Verify checks the token and returns its claims.
func (k Keyring) Verify(token string, now time.Time) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrMalformed
	}
	hdrRaw, err := b64.DecodeString(parts[0])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(hdrRaw, &hdr); err != nil || hasher(hdr.Alg) == nil {
		return Claims{}, ErrMalformed
	}
	body, err := b64.DecodeString(parts[1])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(body, &c); err != nil || c.Sub == "" || c.Aud == "" {
		return Claims{}, ErrMalformed
	}
	key, ok := k.Audiences[c.Aud]
	if !ok {
		return Claims{}, ErrAudience
	}
	if key.Alg != hdr.Alg {
		return Claims{}, ErrSignature
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return Claims{}, ErrMalformed
	}
	mac := hmac.New(hasher(key.Alg), key.Secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Claims{}, ErrSignature
	}
	if !contains(k.Issuers, c.Iss) {
		return Claims{}, ErrIssuer
	}
	if c.Exp == 0 || !now.Before(time.Unix(c.Exp, 0)) {
		return Claims{}, ErrExpired
	}
	return c, nil
}

// Sign produces a token the way the platform does. Used by tests and by the
// gateway stub; production tokens come from AuthServer.
func Sign(c Claims, alg string, secret []byte) string {
	hdr, _ := json.Marshal(map[string]string{"alg": alg, "typ": "JWT"})
	body, _ := json.Marshal(c)
	signables := b64.EncodeToString(hdr) + "." + b64.EncodeToString(body)
	mac := hmac.New(hasher(alg), secret)
	mac.Write([]byte(signables))
	return signables + "." + b64.EncodeToString(mac.Sum(nil))
}

var b64 = base64.RawURLEncoding

func hasher(alg string) func() hash.Hash {
	switch alg {
	case "HS256":
		return sha256.New
	case "HS384":
		return sha512.New384
	case "HS512":
		return sha512.New
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
