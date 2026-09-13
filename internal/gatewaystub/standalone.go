package gatewaystub

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/apostoldevel/go-platform/auth/jwt"
)

// logTB adapts testing.TB for standalone use: failures go to the log.
type logTB struct {
	testing.TB
	cleanups []func()
}

func (l *logTB) Helper()                   {}
func (l *logTB) Errorf(f string, a ...any) { log.Printf("stub: "+f, a...) }
func (l *logTB) Fatalf(f string, a ...any) { log.Printf("stub: "+f, a...) }
func (l *logTB) Fatal(a ...any)            { log.Print(append([]any{"stub: "}, a...)...) }
func (l *logTB) Cleanup(f func())          { l.cleanups = append(l.cleanups, f) }

// Standalone runs the stub outside `go test`, with a fake /oauth2/token that
// issues HS256 tokens of the configured audience — enough to run a real
// module binary end to end without a gateway or an AuthServer.
func Standalone(opts Options) (*Stub, http.Handler) {
	s := newStub(&logTB{}, opts)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("client_id") != opts.Audience || r.Form.Get("client_secret") != opts.Secret {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		exp := 24 * time.Hour
		tok := jwt.Sign(jwt.Claims{Iss: "accounts.test", Aud: opts.Audience, Sub: strings.Repeat("s", 40), Iat: time.Now().Unix(), Exp: time.Now().Add(exp).Unix()}, "HS256", []byte(opts.Secret))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "token_type": "Bearer", "expires_in": int(exp.Seconds())})
	})
	mux.HandleFunc("GET /gateway/list", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"registrations": s.registrations, "heartbeats": len(s.heartbeats), "statuses": s.statuses, "unregisters": s.unregisters, "close_codes": s.closeCodes})
	})
	mux.HandleFunc("/gateway/", s.handle)
	return s, mux
}
