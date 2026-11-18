package cli

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
)

const (
	localGuardSecretArg = "k"
	// where the redirect goes when its target would leave the page
	localGuardLanding = "/dashboard/"
)

// localGuard protects a web page served by the cli on the loopback interface. The page calls the controller with the
// session of the cli, so any local process or web page reaching the port must be kept out. The secret of the url
// opened in the browser becomes a cookie, and the Host check stops DNS rebinding.
type localGuard struct {
	secret       string
	host         string // the address of the listener, set once listening
	cookiePrefix string
}

func newLocalGuard(cookiePrefix string) *localGuard {
	secret := make([]byte, 32)
	// crypto/rand.Read never returns an error, it crashes the program when the system has no randomness
	_, _ = rand.Read(secret)
	return &localGuard{secret: hex.EncodeToString(secret), cookiePrefix: cookiePrefix}
}

// SetHost tells the guard the address of its listener, "127.0.0.1:<port>" by default.
func (g *localGuard) SetHost(host string) {
	g.host = host
}

// URL is the address to open in the browser, it carries the secret.
func (g *localGuard) URL(path string) string {
	return "http://" + g.host + path + "?" + localGuardSecretArg + "=" + g.secret
}

// Wrap answers only the requests with the right Host and the cookie, or trades the secret of the url for it.
func (g *localGuard) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host != g.host {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if req.URL.Query().Has(localGuardSecretArg) {
			g.tradeSecret(w, req)
			return
		}
		if !g.hasSecretCookie(req) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// tradeSecret turns the secret of the url into a cookie, then drops it from the address bar and the history.
func (g *localGuard) tradeSecret(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	if !g.isSecret(query.Get(localGuardSecretArg)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     g.cookieName(),
		Value:    g.secret,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	query.Del(localGuardSecretArg)
	withoutSecret := *req.URL
	withoutSecret.RawQuery = query.Encode()
	target := withoutSecret.RequestURI()
	// The browser reads a path starting with // or /\ as another host, so the redirect would leave the page.
	opensAnotherHost := strings.HasPrefix(target, "//") || strings.HasPrefix(target, `/\`)
	if !strings.HasPrefix(target, "/") || opensAnotherHost {
		target = localGuardLanding
	}
	http.Redirect(w, req, target, http.StatusSeeOther)
}

// cookieName holds the port because browsers share cookies between the ports of a host, so two guards running at once
// would otherwise overwrite each other's cookie.
func (g *localGuard) cookieName() string {
	_, port, _ := net.SplitHostPort(g.host)
	return g.cookiePrefix + port
}

func (g *localGuard) hasSecretCookie(req *http.Request) bool {
	cookie, err := req.Cookie(g.cookieName())
	if err != nil {
		return false
	}
	return g.isSecret(cookie.Value)
}

func (g *localGuard) isSecret(value string) bool {
	return subtle.ConstantTimeCompare([]byte(value), []byte(g.secret)) == 1
}
