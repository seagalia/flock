package cli

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"

	msg "github.com/ArthurHlt/messages"
	"github.com/seagalia/flock/cli/app"
	"golang.org/x/oauth2"
)

// newControllerProxy forwards the calls of the dashboard to the controller of cnf with the session of the cli, or with
// the authorization the browser sends when tokens is nil (standalone mode). The browser speaks the Connect protocol
// over HTTP/1.1, which works on both transports of the controller, so the calls are forwarded as they are.
func newControllerProxy(cnf *app.Config, tokens oauth2.TokenSource) (http.Handler, error) {
	transport, baseURL, err := app.ControllerTransport(cnf)
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid controller address %q: %w", cnf.Host, err)
	}
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		// server streams (logs, status) must reach the browser message by message
		FlushInterval: -1,
		Rewrite: func(r *httputil.ProxyRequest) {
			// SetURL also gives the request the Host of the controller
			r.SetURL(target)
			// the cookie is the secret of the local guard, the controller has no use for it
			r.Out.Header.Del("Cookie")
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			msg.Errorf("Calling the controller for %s failed: %s", req.URL.Path, err)
			http.Error(w, err.Error(), http.StatusBadGateway)
		},
	}

	// the users of a standalone dashboard sign in with their own accounts, the impersonation of the cli is not theirs
	actsAsUser := tokens != nil && cnf.AsUser != nil
	asUser := ""
	if actsAsUser {
		asUser, err = app.AsUserHeader(cnf.AsUser.Username, cnf.AsUser.Groups)
		if err != nil {
			return nil, err
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		forwarded := req.Clone(req.Context())
		// an identity the page or another client sends is never forwarded
		forwarded.Header.Del(app.AsUserMetaKey)
		if tokens == nil {
			proxy.ServeHTTP(w, forwarded)
			return
		}
		// the token source is asked on every call so an expired session renews like in the other commands
		token, err := tokens.Token()
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		// the identity of the call is the one of the cli session, never one the page or another local client sends
		forwarded.Header.Set("Authorization", "Bearer "+token.AccessToken)
		if actsAsUser {
			forwarded.Header.Set(app.AsUserMetaKey, asUser)
		}
		proxy.ServeHTTP(w, forwarded)
	}), nil
}
