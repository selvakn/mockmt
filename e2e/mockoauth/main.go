// Command mockoauth is a disposable OAuth2 authorization-code provider
// standing in for a real identity provider during local end-to-end
// testing. It implements exactly the flow mockmt's own OAuth client
// performs (see internal/mockmt/auth.go) and nothing more: no
// client_id/secret validation, no state validation (the app itself never
// checks state), no token expiry, no persistence. See
// specs/003-e2e-docker-testrig/contracts/mock-oauth.md for the full
// contract.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

type authRequest struct {
	ClientID    string
	RedirectURI string
	State       string
}

// identity is what a code or access token resolves to.
type identity struct {
	Email string
	Name  string
}

var (
	mu     sync.Mutex
	codes  = map[string]identity{}
	tokens = map[string]identity{}
)

var loginTemplate = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html>
<head><title>Mock OAuth Login</title></head>
<body>
  <h1>Mock OAuth Login</h1>
  <p>This is a disposable stand-in identity provider. Type any address and log in as it.</p>
  <form method="POST" action="/authorize">
    <input type="hidden" name="client_id" value="{{.ClientID}}">
    <input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
    <input type="hidden" name="state" value="{{.State}}">
    <label>Email: <input type="text" name="email" value="test@example.com" size="40"></label>
    <button type="submit">Log in</button>
  </form>
</body>
</html>`))

// randomHex returns n bytes of cryptographically random data, hex
// encoded (2n characters). Used for both authorization codes and access
// tokens. Access tokens in particular must be well over 8 characters --
// internal/mockmt/auth.go does an unconditional accessToken[:8] for a log
// line, which panics on shorter input (research R7); 16 bytes (32 hex
// characters) is comfortably clear of that.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("crypto/rand failed: %v", err)
	}
	return hex.EncodeToString(b)
}

func handleAuthorizeGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	data := authRequest{
		ClientID:    q.Get("client_id"),
		RedirectURI: q.Get("redirect_uri"),
		State:       q.Get("state"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := loginTemplate.Execute(w, data); err != nil {
		log.Printf("failed to render login page: %v", err)
	}
}

func handleAuthorizePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	email := r.FormValue("email")
	redirectURI := r.FormValue("redirect_uri")
	state := r.FormValue("state")

	if email == "" || redirectURI == "" {
		http.Error(w, "email and redirect_uri are required", http.StatusBadRequest)
		return
	}

	code := randomHex(16)
	mu.Lock()
	codes[code] = identity{Email: email, Name: email}
	mu.Unlock()

	target, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, "invalid redirect_uri", http.StatusBadRequest)
		return
	}
	qs := target.Query()
	qs.Set("code", code)
	qs.Set("state", state)
	target.RawQuery = qs.Encode()

	http.Redirect(w, r, target.String(), http.StatusFound)
}

func handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	code := r.FormValue("code")

	mu.Lock()
	rec, ok := codes[code]
	if ok {
		delete(codes, code) // single-use
	}
	mu.Unlock()

	// Content-Type MUST be set explicitly, before the first Write. Go's
	// net/http content-sniffs an unset type from the response body, and a
	// body starting with "{" is typically sniffed as text/plain -- which
	// routes golang.org/x/oauth2's client into its form-urlencoded parser
	// instead of its JSON one, silently emptying AccessToken and failing
	// every single login attempt with "server response missing
	// access_token" (research R6). This is the one detail in this whole
	// program that actually matters.
	w.Header().Set("Content-Type", "application/json")

	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}

	accessToken := randomHex(16)
	mu.Lock()
	tokens[accessToken] = rec
	mu.Unlock()

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   3600,
	})
}

func handleUserinfo(w http.ResponseWriter, r *http.Request) {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, prefix) {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}

	mu.Lock()
	rec, ok := tokens[strings.TrimPrefix(auth, prefix)]
	mu.Unlock()
	if !ok {
		http.Error(w, "unknown token", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"sub":            rec.Email,
		"email":          rec.Email,
		"email_verified": true,
		"name":           rec.Name,
		"picture":        "",
	})
}

func main() {
	port := os.Getenv("MOCK_OAUTH_PORT")
	if port == "" {
		port = "9000"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handleAuthorizeGet(w, r)
		case http.MethodPost:
			handleAuthorizePost(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/token", handleToken)
	mux.HandleFunc("/userinfo", handleUserinfo)

	addr := ":" + port
	log.Printf("mock-oauth listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
