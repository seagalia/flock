package cli

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/cli/app"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1/svclookupconnect"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
	"golang.org/x/oauth2"
)

const validBackendFile = `kind: backend
name: web
ip_addr_group:
  ip_addresses:
  - ip: 10.0.0.10
    port: 8080
health_check:
  no_health_check: {}
`

// fakeController is a lookup service standing in for the controller behind the proxy, it records the requests it got.
type fakeController struct {
	svclookupconnect.UnimplementedLookupHandler
	sent     atomic.Int32
	mu       sync.Mutex
	requests []*http.Request
}

func (f *fakeController) ListAgent(context.Context, *svclookup.ListAgentRequest) (*svclookup.ListAgentResponse, error) {
	return &svclookup.ListAgentResponse{}, nil
}

func (f *fakeController) GetLogsStream(_ context.Context, _ *svclookup.GetLogsRequest, stream svclookupconnect.LookupGetLogsStreamServerStream) error {
	for i := 1; i <= 3; i++ {
		if i > 1 {
			time.Sleep(200 * time.Millisecond)
		}
		err := stream.Send(&svclookup.LogsStreamResponse{Event: &svclookup.LogsStreamResponse_LogStream{LogStream: &svclookup.LogsStreamResponse_AgentLogLine{LogLine: fmt.Sprintf("line %d", i)}}})
		if err != nil {
			return err
		}
		f.sent.Add(1)
	}
	return nil
}

func (f *fakeController) lastRequest() *http.Request {
	GinkgoHelper()
	f.mu.Lock()
	defer f.mu.Unlock()
	Expect(f.requests).NotTo(BeEmpty(), "the controller got no request")
	return f.requests[len(f.requests)-1]
}

func (f *fakeController) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// start serves the fake controller over TLS with HTTP/2, like a real controller, or over plain HTTP/1.1.
func (f *fakeController) start(withTLS bool) *httptest.Server {
	server := connect.NewServer()
	svclookupconnect.RegisterLookupHandler(server, f)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r)
		f.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	if withTLS {
		srv.EnableHTTP2 = true
		srv.StartTLS()
	} else {
		srv.Start()
	}
	DeferCleanup(srv.Close)
	return srv
}

// browserTransport plays the browser with the cookie of the guard, and adds the headers a hostile page could send.
type browserTransport struct {
	cookie *http.Cookie
	extra  http.Header
}

func (b *browserTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if b.cookie != nil {
		req.AddCookie(b.cookie)
	}
	maps.Copy(req.Header, b.extra)
	return http.DefaultTransport.RoundTrip(req)
}

var _ = Describe("flock dashboard", func() {
	var (
		controller *fakeController
		cnf        *app.Config
		tokens     oauth2.TokenSource
		cwd        string
		guard      *localGuard
		local      *httptest.Server
	)

	BeforeEach(func() {
		controller = &fakeController{}
		srv := controller.start(true)
		cnf = &app.Config{Host: srv.Listener.Addr().String(), SkipVerify: true}
		tokens = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "cli-token"})
		cwd = GinkgoT().TempDir()
	})

	// serve starts the local server of the command in front of the fake controller.
	serve := func() {
		GinkgoHelper()
		proxy, err := newControllerProxy(cnf, tokens)
		Expect(err).NotTo(HaveOccurred())
		guard = newLocalGuard("flock_dashboard_")
		local = httptest.NewServer(guard.Wrap(dashboardHandler(cwd, proxy)))
		DeferCleanup(local.Close)
		guard.SetHost(strings.TrimPrefix(local.URL, "http://"))
	}

	// serveStandalone starts the server of the standalone mode, which has no guard and no cli session.
	serveStandalone := func() {
		GinkgoHelper()
		proxy, err := newControllerProxy(cnf, nil)
		Expect(err).NotTo(HaveOccurred())
		local = httptest.NewServer(standaloneHandler(cnf.Host, proxy))
		DeferCleanup(local.Close)
	}

	guardCookie := func() *http.Cookie {
		return &http.Cookie{Name: guard.cookieName(), Value: guard.secret}
	}

	browserClient := func(extra http.Header) svclookupconnect.LookupClient {
		httpClient := &http.Client{Transport: &browserTransport{cookie: guardCookie(), extra: extra}}
		return svclookupconnect.NewLookupClient(connect.NewClient(connecthttp.NewTransport(httpClient, local.URL)))
	}

	do := func(method, path string, body io.Reader) (*http.Response, string) {
		GinkgoHelper()
		req, err := http.NewRequest(method, local.URL+path, body)
		Expect(err).NotTo(HaveOccurred())
		req.AddCookie(guardCookie())
		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close() //nolint:errcheck
		content, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		return resp, string(content)
	}

	fileArg := func(path string) string {
		return "/cli/v1/file?path=" + url.QueryEscape(path)
	}

	Describe("guard", func() {
		BeforeEach(serve)

		It("refuses the files and the proxy without the cookie", func() {
			for _, path := range []string{"/cli/v1/info", "/dashboard/", svclookupconnect.LookupListAgentProcedure} {
				resp, err := http.Get(local.URL + path)
				Expect(err).NotTo(HaveOccurred())
				resp.Body.Close() //nolint:errcheck
				Expect(resp.StatusCode).To(Equal(http.StatusForbidden), path)
			}
			Expect(controller.requestCount()).To(BeZero())
		})
	})

	Describe("proxy", func() {
		It("replaces the authorization of the browser by the session of the cli", func() {
			serve()
			evil := http.Header{"Authorization": {"Bearer evil"}, "As_user": {`{"username":"root"}`}}

			_, err := browserClient(evil).ListAgent(context.Background(), &svclookup.ListAgentRequest{})

			Expect(err).NotTo(HaveOccurred())
			headers := controller.lastRequest().Header
			Expect(headers.Values("Authorization")).To(Equal([]string{"Bearer cli-token"}))
			Expect(headers.Get(app.AsUserMetaKey)).To(BeEmpty())
			Expect(headers.Get("Cookie")).To(BeEmpty())
		})

		It("acts as the user of the config", func() {
			cnf.AsUser = &app.AsUser{Username: "alice", Groups: []string{"admins", "devs"}}
			serve()

			_, err := browserClient(http.Header{"As_user": {`{"username":"root"}`}}).
				ListAgent(context.Background(), &svclookup.ListAgentRequest{})

			Expect(err).NotTo(HaveOccurred())
			Expect(controller.lastRequest().Header.Values(app.AsUserMetaKey)).To(HaveLen(1))
			Expect(controller.lastRequest().Header.Get(app.AsUserMetaKey)).
				To(MatchJSON(`{"username":"alice","groups":["admins","devs"]}`))
		})

		It("streams the messages of a server stream one by one", func() {
			serve()
			stream, err := browserClient(nil).GetLogsStream(context.Background(), &svclookup.GetLogsRequest{})
			Expect(err).NotTo(HaveOccurred())
			defer stream.Close() //nolint:errcheck

			first, err := stream.Receive()
			Expect(err).NotTo(HaveOccurred())
			Expect(first.GetLogStream().GetLogLine()).To(Equal("line 1"))
			Expect(controller.sent.Load()).To(BeNumerically("<", 3), "the first line must arrive before the stream ends")
			lines := []string{first.GetLogStream().GetLogLine()}
			for {
				resp, err := stream.Receive()
				if errors.Is(err, io.EOF) {
					break
				}
				Expect(err).NotTo(HaveOccurred())
				lines = append(lines, resp.GetLogStream().GetLogLine())
			}
			Expect(lines).To(Equal([]string{"line 1", "line 2", "line 3"}))
			Expect(controller.lastRequest().ProtoMajor).To(Equal(2))
		})

		It("reaches a controller in HTTP/1 mode with the http scheme", func() {
			plain := controller.start(false)
			cnf = &app.Config{Host: plain.Listener.Addr().String(), Http1Transport: true, Http1Scheme: "http"}
			serve()

			_, err := browserClient(nil).ListAgent(context.Background(), &svclookup.ListAgentRequest{})

			Expect(err).NotTo(HaveOccurred())
			Expect(controller.lastRequest().ProtoMajor).To(Equal(1))
			Expect(controller.lastRequest().Host).To(Equal(cnf.Host))
		})

		It("answers 502 when the controller is unreachable", func() {
			gone := controller.start(true)
			cnf = &app.Config{Host: gone.Listener.Addr().String(), SkipVerify: true}
			gone.Close()
			serve()

			resp, body := do(http.MethodPost, svclookupconnect.LookupListAgentProcedure, nil)

			Expect(resp.StatusCode).To(Equal(http.StatusBadGateway))
			Expect(body).To(ContainSubstring("connection refused"))
		})

		It("sends the root to the dashboard instead of the controller", func() {
			serve()

			resp, _ := do(http.MethodGet, "/", nil)

			Expect(resp.Request.URL.Path).To(Equal("/dashboard/"))
			Expect(controller.requestCount()).To(BeZero())
		})

		It("answers 401 when the session has no token", func() {
			tokens = failingTokenSource{}
			serve()

			resp, body := do(http.MethodPost, svclookupconnect.LookupListAgentProcedure, nil)

			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
			Expect(body).To(ContainSubstring("session expired"))
			Expect(controller.requestCount()).To(BeZero())
		})
	})

	Describe("standalone", func() {
		BeforeEach(serveStandalone)

		standaloneClient := func(headers http.Header) svclookupconnect.LookupClient {
			httpClient := &http.Client{Transport: &browserTransport{extra: headers}}
			return svclookupconnect.NewLookupClient(connect.NewClient(connecthttp.NewTransport(httpClient, local.URL)))
		}

		get := func(path string) (*http.Response, string) {
			GinkgoHelper()
			resp, err := http.Get(local.URL + path)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close() //nolint:errcheck
			content, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred())
			return resp, string(content)
		}

		It("forwards the authorization of the browser and drops its cookie and as_user", func() {
			browser := http.Header{
				"Authorization": {"Bearer browser-token"},
				"Cookie":        {"session=secret"},
				"As_user":       {`{"username":"root"}`},
			}

			_, err := standaloneClient(browser).ListAgent(context.Background(), &svclookup.ListAgentRequest{})

			Expect(err).NotTo(HaveOccurred())
			headers := controller.lastRequest().Header
			Expect(headers.Values("Authorization")).To(Equal([]string{"Bearer browser-token"}))
			Expect(headers.Get("Cookie")).To(BeEmpty())
			Expect(headers.Get(app.AsUserMetaKey)).To(BeEmpty())
		})

		It("never acts as the user of the cli config", func() {
			cnf.AsUser = &app.AsUser{Username: "alice"}
			serveStandalone()

			_, err := standaloneClient(nil).ListAgent(context.Background(), &svclookup.ListAgentRequest{})

			Expect(err).NotTo(HaveOccurred())
			Expect(controller.lastRequest().Header.Get(app.AsUserMetaKey)).To(BeEmpty())
			Expect(controller.lastRequest().Header.Get("Authorization")).To(BeEmpty())
		})

		It("has no route of the local mode", func() {
			for _, path := range []string{"/cli/v1/info", fileArg("res.yml")} {
				resp, _ := get(path)
				Expect(resp.StatusCode).To(Equal(http.StatusNotFound), path)
			}
			Expect(controller.requestCount()).To(BeZero())
		})

		It("writes no file of the builder", func() {
			req, err := http.NewRequest(http.MethodPut, local.URL+fileArg("res.yml"), strings.NewReader(validBackendFile))
			Expect(err).NotTo(HaveOccurred())

			resp, err := http.DefaultClient.Do(req)

			Expect(err).NotTo(HaveOccurred())
			resp.Body.Close() //nolint:errcheck
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
			entries, err := os.ReadDir(cwd)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
			Expect(controller.requestCount()).To(BeZero())
		})

		It("gives the controller address to the page", func() {
			resp, body := get("/standalone/v1/info")

			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(body).To(MatchJSON(fmt.Sprintf(`{"controller": %q}`, cnf.Host)))
			Expect(controller.requestCount()).To(BeZero())
		})

		It("sends the root to the dashboard", func() {
			resp, _ := get("/")

			Expect(resp.Request.URL.Path).To(Equal("/dashboard/"))
			Expect(controller.requestCount()).To(BeZero())
		})
	})

	Describe("files", func() {
		BeforeEach(serve)

		It("gives the working directory", func() {
			resp, body := do(http.MethodGet, "/cli/v1/info", nil)

			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(body).To(MatchJSON(fmt.Sprintf(`{"cwd": %q}`, cwd)))
		})

		It("keeps an unknown route of the local mode away from the controller", func() {
			for _, path := range []string{"/cli/v1/unknown", "/cli/v1/info/more"} {
				resp, _ := do(http.MethodGet, path, nil)

				Expect(resp.StatusCode).To(Equal(http.StatusNotFound), path)
			}
			Expect(controller.requestCount()).To(BeZero())
		})

		It("reads no file, the builder only writes new ones", func() {
			Expect(os.WriteFile(filepath.Join(cwd, "res.yml"), []byte(validBackendFile), 0644)).To(Succeed())

			resp, body := do(http.MethodGet, fileArg("res.yml"), nil)

			Expect(resp.StatusCode).To(Equal(http.StatusMethodNotAllowed))
			Expect(body).NotTo(ContainSubstring(validBackendFile))
			Expect(controller.requestCount()).To(BeZero())
		})

		It("writes a new file at an absolute path", func() {
			path := filepath.Join(cwd, "new.yml")

			resp, _ := do(http.MethodPut, fileArg(path), strings.NewReader(validBackendFile))

			Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
			Expect(os.ReadFile(path)).To(BeEquivalentTo(validBackendFile))
			entries, err := os.ReadDir(cwd)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1), "no temp file is left behind")
		})

		It("overwrites an existing file only when asked", func() {
			path := filepath.Join(cwd, "res.yml")
			Expect(os.WriteFile(path, []byte("old"), 0644)).To(Succeed())

			resp, body := do(http.MethodPut, fileArg("res.yml"), strings.NewReader(validBackendFile))
			Expect(resp.StatusCode).To(Equal(http.StatusConflict))
			Expect(body).To(MatchJSON(`{"error":"file exists"}`))
			Expect(os.ReadFile(path)).To(BeEquivalentTo("old"))

			resp, _ = do(http.MethodPut, fileArg("res.yml")+"&overwrite=true", strings.NewReader(validBackendFile))
			Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
			Expect(os.ReadFile(path)).To(BeEquivalentTo(validBackendFile))
			entries, err := os.ReadDir(cwd)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1), "no temp file is left behind")
		})

		It("keeps the mode of an overwritten file and the symlink pointing to it", func() {
			target := filepath.Join(cwd, "real.yml")
			Expect(os.WriteFile(target, []byte("old"), 0600)).To(Succeed())
			link := filepath.Join(cwd, "res.yml")
			Expect(os.Symlink(target, link)).To(Succeed())

			resp, _ := do(http.MethodPut, fileArg("res.yml")+"&overwrite=true", strings.NewReader(validBackendFile))

			Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
			linkInfo, err := os.Lstat(link)
			Expect(err).NotTo(HaveOccurred())
			Expect(linkInfo.Mode() & os.ModeSymlink).NotTo(BeZero())
			Expect(os.ReadFile(target)).To(BeEquivalentTo(validBackendFile))
			targetInfo, err := os.Stat(target)
			Expect(err).NotTo(HaveOccurred())
			Expect(targetInfo.Mode().Perm()).To(Equal(os.FileMode(0600)))
		})

		It("answers the problems of the content and leaves the file as it was", func() {
			path := filepath.Join(cwd, "res.yml")
			Expect(os.WriteFile(path, []byte("old"), 0644)).To(Succeed())
			invalid := "kind: vs\nname: web\nport: 0\ntcp_route_config:\n  backend_uuid: x\n"

			resp, body := do(http.MethodPut, fileArg("res.yml")+"&overwrite=true", strings.NewReader(invalid))

			Expect(resp.StatusCode).To(Equal(http.StatusUnprocessableEntity))
			var answer struct {
				Problems []map[string]any `json:"problems"`
			}
			Expect(json.Unmarshal([]byte(body), &answer)).To(Succeed())
			Expect(answer.Problems).NotTo(BeEmpty())
			Expect(answer.Problems[0]).To(HaveKeyWithValue("file", "res.yml"))
			Expect(answer.Problems[0]).To(HaveKeyWithValue("index", BeEquivalentTo(1)))
			Expect(answer.Problems[0]).To(HaveKey("message"))
			fields := make([]any, 0, len(answer.Problems))
			for _, problem := range answer.Problems {
				fields = append(fields, problem["field"])
			}
			Expect(fields).To(ContainElement(ContainSubstring("port")))
			Expect(os.ReadFile(path)).To(BeEquivalentTo("old"))
		})

		It("refuses a body over 1 MiB", func() {
			resp, _ := do(http.MethodPut, fileArg("big.yml"), bytes.NewReader(make([]byte, maxResourceFileSize+1)))

			Expect(resp.StatusCode).To(Equal(http.StatusRequestEntityTooLarge))
			Expect(filepath.Join(cwd, "big.yml")).NotTo(BeAnExistingFile())
		})
	})

	Describe("command", func() {
		var (
			runner *commandRunner
			stop   context.CancelFunc
			hasTTY bool
		)

		BeforeEach(func() {
			previousBuilt, previousTTY := dashboardIsBuilt, stdinIsTerminal
			DeferCleanup(func() {
				dashboardIsBuilt, stdinIsTerminal = previousBuilt, previousTTY
			})
			dashboardIsBuilt = func() bool { return true }
			hasTTY = true
			stdinIsTerminal = func() bool { return hasTTY }

			runner = newCommandRunner()
			// the command stops at once unless a spec keeps it running, as if Ctrl-C came right after the start
			runner.cancel()
			Expect(app.WriteConfig(runner.configPath, &app.Config{Host: cnf.Host, Username: "alice", SkipVerify: true})).
				To(Succeed())
		})

		It("refuses to start the local mode without a session", func() {
			_, err := runner.run("dashboard", "--no-open")

			Expect(err).To(MatchError("you are not logged in, run flock login"))
		})

		It("starts the local mode with the session of the cli", func() {
			GinkgoT().Setenv("FLOCK_TOKEN", "cli-token")

			output, err := runner.run("dashboard", "--no-open")

			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("Local dashboard of " + cnf.Host + " acting as the user of the cli session"))
			Expect(output).NotTo(ContainSubstring("alice"), "FLOCK_TOKEN may belong to another user than the config")
		})

		It("names the user it acts as", func() {
			GinkgoT().Setenv("FLOCK_TOKEN", "cli-token")
			config := &app.Config{Host: cnf.Host, Username: "alice", AsUser: &app.AsUser{Username: "bob"}}
			Expect(app.WriteConfig(runner.configPath, config)).To(Succeed())

			output, err := runner.run("dashboard", "--no-open")

			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("Local dashboard of " + cnf.Host + " acting as bob"))
		})

		It("starts the standalone mode with no session", func() {
			output, err := runner.run("dashboard", "--standalone", "--no-open")

			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(MatchRegexp(`Standalone dashboard of %s on http://127\.0\.0\.1:\d+/dashboard/`, cnf.Host))
			Expect(output).NotTo(ContainSubstring("plain HTTP"))
		})

		It("starts the local mode and opens the browser even when stdin is no terminal", func() {
			hasTTY = false
			GinkgoT().Setenv("FLOCK_TOKEN", "cli-token")
			var opened []string
			previousOpen := openBrowser
			DeferCleanup(func() { openBrowser = previousOpen })
			openBrowser = func(pageURL string) { opened = append(opened, pageURL) }

			output, err := runner.run("dashboard")

			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("Local dashboard of " + cnf.Host))
			Expect(opened).To(ConsistOf(HavePrefix("http://127.0.0.1:")))
		})

		It("opens no browser for a standalone mode without a terminal", func() {
			hasTTY = false
			var opened []string
			previousOpen := openBrowser
			DeferCleanup(func() { openBrowser = previousOpen })
			openBrowser = func(pageURL string) { opened = append(opened, pageURL) }

			_, err := runner.run("dashboard", "--standalone")

			Expect(err).NotTo(HaveOccurred())
			Expect(opened).To(BeEmpty())
		})

		It("refuses a local mode on another address than loopback", func() {
			GinkgoT().Setenv("FLOCK_TOKEN", "cli-token")

			_, err := runner.run("dashboard", "--no-open", "--port", "0.0.0.0:0")

			Expect(err).To(MatchError("the dashboard listens on a loopback address only, got 0.0.0.0:0"))
		})

		It("warns when the standalone mode serves plain HTTP beyond loopback", func() {
			output, err := runner.run("dashboard", "--standalone", "--no-open", "--port", "0.0.0.0:0")

			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(MatchRegexp(
				`The dashboard serves plain HTTP on \S+, use --tls-cert and --tls-key to serve TLS\.`))
		})

		It("refuses a certificate without its key", func() {
			_, err := runner.run("dashboard", "--standalone", "--tls-cert", "cert.pem")

			Expect(err).To(MatchError("--tls-cert and --tls-key must be used together"))
		})

		It("refuses TLS in the local mode", func() {
			_, err := runner.run("dashboard", "--tls-cert", "cert.pem", "--tls-key", "key.pem")

			Expect(err).To(MatchError("--tls-cert and --tls-key are for the standalone mode, add --standalone"))
		})

		It("serves HTTPS with the given certificate", func() {
			certFile, keyFile, clientTLS := writeTestTLSPair(GinkgoT().TempDir())
			addr := freeLoopbackAddr()
			runner.ctx, stop = context.WithCancel(context.Background())
			DeferCleanup(stop)
			done := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				_, err := runner.run("dashboard", "--standalone", "--no-open", "--port", addr, "--tls-cert", certFile, "--tls-key", keyFile)
				done <- err
			}()

			client := &http.Client{Transport: &http.Transport{TLSClientConfig: clientTLS}}
			var body []byte
			Eventually(func() error {
				resp, err := client.Get("https://" + addr + "/standalone/v1/info")
				if err != nil {
					return err
				}
				defer resp.Body.Close() //nolint:errcheck
				body, err = io.ReadAll(resp.Body)
				return err
			}).Should(Succeed())
			Expect(body).To(MatchJSON(fmt.Sprintf(`{"controller": %q}`, cnf.Host)))

			stop()
			Eventually(done).Should(Receive(BeNil()))
		})

		It("refuses to run when the dashboard is not built", func() {
			dashboardIsBuilt = func() bool { return false }

			_, err := runner.run("dashboard", "--no-open")

			Expect(err).To(MatchError("the dashboard is not built into this binary, run make webui"))
		})
	})
})

// writeTestTLSPair writes the certificate and key of a server on 127.0.0.1 in dir, and gives the TLS config of a
// client trusting it.
func writeTestTLSPair(dir string) (certFile, keyFile string, clientTLS *tls.Config) {
	GinkgoHelper()
	serverTLS, clientTLS := rpctest.NewTLS("")
	cert := serverTLS.Certificates[0]
	keyDER, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	Expect(err).NotTo(HaveOccurred())
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	Expect(os.WriteFile(certFile, certPEM, 0600)).To(Succeed())
	Expect(os.WriteFile(keyFile, keyPEM, 0600)).To(Succeed())
	return certFile, keyFile, clientTLS
}

// freeLoopbackAddr gives a port free a moment ago, for a command that only takes a listen address.
func freeLoopbackAddr() string {
	GinkgoHelper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	addr := ln.Addr().String()
	Expect(ln.Close()).To(Succeed())
	return addr
}

type failingTokenSource struct{}

func (failingTokenSource) Token() (*oauth2.Token, error) {
	return nil, errors.New("session expired, run flock login")
}
