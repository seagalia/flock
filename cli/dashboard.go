package cli

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	msg "github.com/ArthurHlt/messages"
	"github.com/mattn/go-isatty"
	"github.com/pkg/browser"
	"github.com/seagalia/flock/cli/app"
	"github.com/seagalia/flock/webui"
)

const dashboardShutdownTimeout = 5 * time.Second

// dashboardIsBuilt is a variable so specs run the command whether the dashboard of their binary is built or not.
var dashboardIsBuilt = webui.IsBuilt

// stdinIsTerminal tells whether the standalone mode may open a browser, a variable so specs choose whether they run
// from a terminal.
var stdinIsTerminal = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) }

type DashboardCmd struct {
	ListenAddr string `short:"p" name:"port" help:"Listen address, on loopback in local mode" default:"127.0.0.1:0"`
	NoOpen     bool   `name:"no-open" help:"Print the url without opening the browser (the standalone mode opens it only from a terminal)"`
	Standalone bool   `name:"standalone" help:"Serve the dashboard to users who sign in with their own accounts, without the cli session"`
	TLSCert    string `name:"tls-cert" and:"tls" type:"path" help:"Certificate file (PEM) of the standalone mode, with --tls-key"`
	TLSKey     string `name:"tls-key" and:"tls" type:"path" help:"Private key file (PEM) of --tls-cert"`
}

// Run loads its config itself instead of being given a client, because the standalone mode needs no session.
func (c *DashboardCmd) Run(ctx context.Context) error {
	if !dashboardIsBuilt() {
		return errors.New("the dashboard is not built into this binary, run make webui")
	}
	cnf, err := app.RetrieveConfig(ExpandConfigPath())
	if err != nil {
		return err
	}
	if c.Standalone {
		return c.runStandalone(ctx, cnf)
	}
	if c.TLSCert != "" {
		return errors.New("--tls-cert and --tls-key are for the standalone mode, add --standalone")
	}
	return c.runLocal(ctx, cnf)
}

func (c *DashboardCmd) runLocal(ctx context.Context, cnf *app.Config) error {
	tokens, err := app.SessionTokenSource(ctx, cnf)
	if err != nil {
		return err
	}
	// without a session the page could only show a login form whose calls the proxy refuses as well
	if _, err := tokens.Token(); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	proxy, err := newControllerProxy(cnf, tokens)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", c.ListenAddr)
	if err != nil {
		return err
	}
	// the proxy carries every right of the session, no other machine may reach it
	if !ln.Addr().(*net.TCPAddr).IP.IsLoopback() {
		ln.Close() //nolint:errcheck
		return fmt.Errorf("the dashboard listens on a loopback address only, got %s", c.ListenAddr)
	}
	guard := newLocalGuard("flock_dashboard_")
	guard.SetHost(ln.Addr().String())
	server := &http.Server{Handler: guard.Wrap(dashboardHandler(cwd, proxy)), ReadHeaderTimeout: 10 * time.Second}

	// the username of the config is the one of the session file, FLOCK_TOKEN may belong to anyone and an OIDC login
	// leaves no username
	actingAs := cnf.Username
	usesOtherToken := cnf.Token != ""
	if usesOtherToken || actingAs == "" {
		actingAs = "the user of the cli session"
	}
	if cnf.AsUser != nil {
		actingAs = cnf.AsUser.Username
	}
	pageURL := guard.URL("/dashboard/")
	msg.Infof("Local dashboard of %s acting as %s", msg.Cyan(cnf.Host), msg.Cyan(actingAs))
	msg.Infof("Open %s, Ctrl-C to stop", msg.Cyan(pageURL))
	if !c.NoOpen {
		openBrowser(pageURL)
	}
	return serveUntilStopped(ctx, server, ln)
}

func (c *DashboardCmd) runStandalone(ctx context.Context, cnf *app.Config) error {
	proxy, err := newControllerProxy(cnf, nil)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: standaloneHandler(cnf.Host, proxy), ReadHeaderTimeout: 10 * time.Second}
	// the pair is read before listening so a wrong file fails the command instead of every connection
	scheme := "http"
	if c.TLSCert != "" {
		pair, err := tls.LoadX509KeyPair(c.TLSCert, c.TLSKey)
		if err != nil {
			return err
		}
		server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
		scheme = "https"
	}

	ln, err := net.Listen("tcp", c.ListenAddr)
	if err != nil {
		return err
	}
	addr := ln.Addr().String()
	// users send their passwords and tokens through it
	plainBeyondLoopback := server.TLSConfig == nil && !ln.Addr().(*net.TCPAddr).IP.IsLoopback()
	if plainBeyondLoopback {
		msg.Warningf("The dashboard serves plain HTTP on %s, use --tls-cert and --tls-key to serve TLS.", addr)
	}
	pageURL := fmt.Sprintf("%s://%s/dashboard/", scheme, addr)
	msg.Infof("Standalone dashboard of %s on %s", msg.Cyan(cnf.Host), msg.Cyan(pageURL))
	msg.Info("Ctrl-C to stop")
	// a run without a terminal is a server, it has no browser to open
	shouldOpen := !c.NoOpen && stdinIsTerminal()
	if shouldOpen {
		openBrowser(pageURL)
	}
	return serveUntilStopped(ctx, server, ln)
}

// openBrowser is a variable so specs see the url instead of a browser opening
var openBrowser = func(pageURL string) {
	err := browser.OpenURL(pageURL)
	if err != nil {
		msg.Errorf("Failed to open the browser (%s), open %s yourself", err.Error(), msg.Cyan(pageURL))
	}
}

// serveUntilStopped serves on ln, over TLS when the server has a TLS config, until ctx ends (Ctrl-C).
func serveUntilStopped(ctx context.Context, server *http.Server, ln net.Listener) error {
	served := make(chan error, 1)
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		if server.TLSConfig != nil {
			// ServeTLS takes the certificates of TLSConfig when given no file, and adds HTTP/2 to it
			served <- server.ServeTLS(ln, "", "")
			return
		}
		served <- server.Serve(ln)
	}()

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	// open log streams would keep Shutdown waiting, so it gives up after a while
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dashboardShutdownTimeout)
	defer cancel()
	err := server.Shutdown(shutdownCtx)
	if errors.Is(err, context.DeadlineExceeded) {
		err = server.Close()
	}
	// Serve returns once the server is shut down, waiting for it leaves no goroutine on a closed listener
	<-serveDone
	return err
}

// dashboardHandler serves the dashboard of the local mode, the file endpoints of its builder and sends every other
// request to proxy.
func dashboardHandler(cwd string, proxy http.Handler) http.Handler {
	files := resourceFiles{cwd: cwd}
	mux := http.NewServeMux()
	webui.Mount(mux, "/dashboard", webui.Browser())
	mux.HandleFunc("GET /cli/v1/info", files.info)
	mux.HandleFunc("PUT /cli/v1/file", files.write)
	// the catch-all proxy would otherwise send a GET of the path to the controller, with the token of the cli session
	mux.HandleFunc("/cli/v1/file", onlyPut)
	// an unknown route of the local mode must not reach the controller either, it would carry the cli token
	mux.Handle("/cli/v1/", http.NotFoundHandler())
	// the root of the local server is the page, the controller only has a 404 for it
	mux.Handle("GET /{$}", http.RedirectHandler("/dashboard/", http.StatusSeeOther))
	mux.Handle("/", proxy)
	return mux
}

// standaloneHandler serves the dashboard of the standalone mode and sends every other request to proxy. The page
// finds the mode it runs in from the info route it gets an answer from.
func standaloneHandler(controllerHost string, proxy http.Handler) http.Handler {
	mux := http.NewServeMux()
	webui.Mount(mux, "/dashboard", webui.Browser())
	mux.HandleFunc("GET /standalone/v1/info", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"controller": controllerHost})
	})
	// the files of the local mode stay on the machine of the cli, other users of the page have no access to them
	mux.Handle("/cli/v1/", http.NotFoundHandler())
	mux.Handle("GET /{$}", http.RedirectHandler("/dashboard/", http.StatusSeeOther))
	mux.Handle("/", proxy)
	return mux
}
