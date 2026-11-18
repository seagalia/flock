// Package fakeoidc is an OIDC provider for the specs of the cli login: discovery, keys, an authorize endpoint that
// redirects straight to the callback, the device grant and a token endpoint checking PKCE and client credentials.
package fakeoidc

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	ClientId     = "flock-cli"
	ClientSecret = "machine-secret"
	UserCode     = "ABCD-EFGH"
)

type Provider struct {
	server *httptest.Server
	key    *rsa.PrivateKey

	mu sync.Mutex
	// ForgedState is sent back to the callback instead of the state of the request when set
	ForgedState string
	// NoIdTokenOnRefresh answers a refresh without id_token, as providers do that only renew the access token
	NoIdTokenOnRefresh bool
	// PendingPolls is the number of device token polls answered authorization_pending
	PendingPolls int
	// NoExpiresIn answers the client credentials grant without expires_in, the expiry is then only in the token
	NoExpiresIn bool
	// IdTokenTTL is the lifetime of the ID tokens issued, an hour when zero
	IdTokenTTL time.Duration

	pendingCodes map[string]authRequest
	// deviceChallenge is the PKCE challenge of the last device authorization, checked like Keycloak does for a client
	// requiring PKCE
	deviceChallenge string
	grants          []string
	issued          int
	lastIdToken     string
}

type authRequest struct {
	challenge string
	nonce     string
}

// New starts the provider, close it with Close
func New() (*Provider, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	p := &Provider{key: key, pendingCodes: map[string]authRequest{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /keys", p.keys)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /device", p.device)
	mux.HandleFunc("POST /token", p.token)
	p.server = httptest.NewServer(mux)
	return p, nil
}

func (p *Provider) Close() {
	p.server.Close()
}

func (p *Provider) Issuer() string {
	return p.server.URL
}

// Grants lists the grant types the token endpoint received, in order
func (p *Provider) Grants() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.grants...)
}

func (p *Provider) LastIdToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastIdToken
}

// SignIdToken gives an ID token of the provider for the cli client expiring at exp
func (p *Provider) SignIdToken(exp time.Time) string {
	return p.sign(jwt.MapClaims{"sub": "alice", "aud": ClientId, "exp": exp.Unix()})
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.server.URL,
		"jwks_uri":                              p.server.URL + "/keys",
		"authorization_endpoint":                p.server.URL + "/authorize",
		"device_authorization_endpoint":         p.server.URL + "/device",
		"token_endpoint":                        p.server.URL + "/token",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *Provider) keys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": "key-1", "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(p.key.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(p.key.E)).Bytes()),
	}}})
}

// authorize logs the user in at once and redirects to the callback with a code
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != ClientId {
		http.Error(w, "S256 PKCE and the cli client id are required", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.issued++
	code := fmt.Sprintf("code-%d", p.issued)
	p.pendingCodes[code] = authRequest{challenge: q.Get("code_challenge"), nonce: q.Get("nonce")}
	state := q.Get("state")
	if p.ForgedState != "" {
		state = p.ForgedState
	}
	p.mu.Unlock()

	callback, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	callback.RawQuery = url.Values{"code": {code}, "state": {state}}.Encode()
	http.Redirect(w, r, callback.String(), http.StatusFound)
}

func (p *Provider) device(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil || r.PostForm.Get("code_challenge_method") != "S256" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	p.mu.Lock()
	p.deviceChallenge = r.PostForm.Get("code_challenge")
	p.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":               "device-code",
		"user_code":                 UserCode,
		"verification_uri":          p.server.URL + "/activate",
		"verification_uri_complete": p.server.URL + "/activate?user_code=" + UserCode,
		"expires_in":                60,
		"interval":                  1,
	})
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	grant := r.PostForm.Get("grant_type")
	p.grants = append(p.grants, grant)

	switch grant {
	case "authorization_code":
		pending, found := p.pendingCodes[r.PostForm.Get("code")]
		delete(p.pendingCodes, r.PostForm.Get("code"))
		isVerified := found && isPkceVerified(r.PostForm.Get("code_verifier"), pending.challenge)
		if !isVerified {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		p.writeTokens(w, pending.nonce, true)
	case "urn:ietf:params:oauth:grant-type:device_code":
		if !isPkceVerified(r.PostForm.Get("code_verifier"), p.deviceChallenge) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
			return
		}
		if p.PendingPolls > 0 {
			p.PendingPolls--
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "authorization_pending"})
			return
		}
		p.writeTokens(w, "", true)
	case "refresh_token":
		p.writeTokens(w, "", !p.NoIdTokenOnRefresh)
	case "client_credentials":
		clientId, secret, _ := r.BasicAuth()
		if clientId != ClientId || secret != ClientSecret {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
			return
		}
		p.issued++
		resp := map[string]any{
			"access_token": p.sign(jwt.MapClaims{"sub": ClientId, "aud": "flock", "n": p.issued}),
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		if p.NoExpiresIn {
			delete(resp, "expires_in")
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
	}
}

// writeTokens answers a user grant, p.mu is held
func (p *Provider) writeTokens(w http.ResponseWriter, nonce string, withIdToken bool) {
	p.issued++
	resp := map[string]any{
		"access_token":  fmt.Sprintf("access-%d", p.issued),
		"refresh_token": fmt.Sprintf("refresh-%d", p.issued),
		"token_type":    "Bearer",
		"expires_in":    3600,
	}
	if withIdToken {
		ttl := p.IdTokenTTL
		if ttl == 0 {
			ttl = time.Hour
		}
		claims := jwt.MapClaims{"sub": "alice", "aud": ClientId, "exp": time.Now().Add(ttl).Unix(), "n": p.issued}
		if nonce != "" {
			claims["nonce"] = nonce
		}
		p.lastIdToken = p.sign(claims)
		resp["id_token"] = p.lastIdToken
	}
	writeJSON(w, http.StatusOK, resp)
}

// sign gives a token of the provider, given claims override the issuer and the dates
func (p *Provider) sign(claims jwt.MapClaims) string {
	now := time.Now()
	full := jwt.MapClaims{"iss": p.server.URL, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	maps.Copy(full, claims)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, full)
	tok.Header["kid"] = "key-1"
	signed, err := tok.SignedString(p.key)
	if err != nil {
		panic(err)
	}
	return signed
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func isPkceVerified(verifier, challenge string) bool {
	verifierHash := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(verifierHash[:]) == challenge
}
