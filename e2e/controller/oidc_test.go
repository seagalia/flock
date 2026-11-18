//go:build e2e

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/e2e/controller/env"
)

var _ = Describe("OIDC", Ordered, func() {
	var (
		g        *env.Controller
		provider *env.Oauth2
	)

	BeforeAll(func(ctx SpecContext) {
		var err error
		provider, err = e2eEnv.StartOauth2(ctx)
		Expect(err).NotTo(HaveOccurred())
		g = startController(ctx, env.ControllerOptions{Name: "controller-oidc", Store: env.StoreSqlite, Oidc: provider})
	}, NodeTimeout(10*time.Minute))

	BeforeEach(func() {
		registerControllerForDump(g)
	})

	It("tells clients the provider to log in with and refuses passwords", func(ctx SpecContext) {
		anonymous, err := g.Anonymous(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(anonymous.Close)

		serviceAuth, err := anonymous.Login.GetServiceAuth(ctx, &emptypb.Empty{})
		Expect(err).NotTo(HaveOccurred())
		Expect(serviceAuth.GetType()).To(Equal("OIDC"))
		Expect(serviceAuth.GetOidc().GetIssuerUrl()).To(Equal(provider.IssuerURL()))
		Expect(serviceAuth.GetOidc().GetClientId()).To(Equal(env.OidcClientId))

		_, err = g.LoginAs(ctx, "alice", "any-password")
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeFailedPrecondition), "%v", err)
	}, NodeTimeout(time.Minute))

	It("logs a user in with an ID token", func(ctx SpecContext) {
		alice := loginOidc(ctx, g, provider, "alice")

		me, err := alice.Login.Me(ctx, &emptypb.Empty{})
		Expect(err).NotTo(HaveOccurred())
		Expect(me.GetUsername()).To(Equal("alice"))
		Expect(me.GetGroups()).To(ConsistOf(env.Oauth2Users["alice"]))

		_, err = alice.Backend.ListBackend(ctx, &svcbackend.ListBackendRequest{})
		Expect(err).NotTo(HaveOccurred())
	}, NodeTimeout(time.Minute))

	It("logs a machine in with an access token", func(ctx SpecContext) {
		accessToken, err := provider.AccessToken(ctx, "machine-1")
		Expect(err).NotTo(HaveOccurred())
		machine, err := g.LoginWithBearer(ctx, accessToken)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(machine.Close)

		me, err := machine.Login.Me(ctx, &emptypb.Empty{})
		Expect(err).NotTo(HaveOccurred())
		Expect(me.GetUsername()).To(Equal("machine-1"))
	}, NodeTimeout(time.Minute))

	It("refuses an ID token issued for another client", func(ctx SpecContext) {
		idToken, err := provider.IdTokenForClient(ctx, "other-client", "alice", env.Oauth2Users["alice"])
		Expect(err).NotTo(HaveOccurred())
		impostor, err := g.LoginWithBearer(ctx, idToken)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(impostor.Close)

		_, err = impostor.Login.Me(ctx, &emptypb.Empty{})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
	}, NodeTimeout(time.Minute))

	It("refuses a user calling itself break-glass", func(ctx SpecContext) {
		idToken, err := provider.IdToken(ctx, svclogin.BreakGlassUsername, []string{env.AdminGroup})
		Expect(err).NotTo(HaveOccurred())
		impostor, err := g.LoginWithBearer(ctx, idToken)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(impostor.Close)

		_, err = impostor.Login.Me(ctx, &emptypb.Empty{})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)
	}, NodeTimeout(time.Minute))

	It("logs in with the cli through the browser loopback", func(ctx SpecContext) {
		cli := newHostCli(ctx, g, provider)

		login := cli.command(ctx, "login", "--callback-port", strconv.Itoa(freePort()))
		// the cli prints its messages on stdout
		stdout, err := login.StdoutPipe()
		Expect(err).NotTo(HaveOccurred())
		var stderr bytes.Buffer
		login.Stderr = &stderr
		Expect(login.Start()).To(Succeed())
		var output bytes.Buffer
		loginUrl := make(chan string, 1)
		loginDone := make(chan error, 1)
		go func() {
			loginUrl <- readLoginUrl(io.TeeReader(stdout, &output))
			_, _ = io.Copy(&output, stdout)
			loginDone <- login.Wait()
		}()

		// what the browser does: the local server of the cli redirects to the provider, which redirects back to the
		// callback without asking anything
		var url string
		Eventually(loginUrl, time.Minute).Should(Receive(&url))
		if url == "" {
			exitErr := <-loginDone
			Fail(fmt.Sprintf("the cli printed no login url (%v): %s%s", exitErr, output.String(), stderr.String()))
		}
		resp, err := provider.HTTPClient(ctx).Get(url)
		Expect(err).NotTo(HaveOccurred())
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		Expect(resp.StatusCode).To(Equal(http.StatusOK), "callback %s answered: %s", resp.Request.URL, body)

		var loginErr error
		Eventually(loginDone, time.Minute).Should(Receive(&loginErr))
		Expect(loginErr).NotTo(HaveOccurred(), "flock login: %s%s", output.String(), stderr.String())

		meOut, err := cli.command(ctx, "me", "--json").CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "flock me: %s", meOut)
		me := struct {
			Username string   `json:"username"`
			Groups   []string `json:"groups"`
		}{}
		Expect(json.Unmarshal(meOut, &me)).To(Succeed(), string(meOut))
		Expect(me.Username).To(Equal(env.Oauth2CodeUser))
		Expect(me.Groups).To(ConsistOf(env.Oauth2CodeGroups))
	}, NodeTimeout(5*time.Minute))
})

// hostCli runs the flock binary of the host against a controller of the suite, with a home of its own so it never
// reads the session of the user
type hostCli struct {
	bin   string
	home  string
	host  string
	proxy string
}

// newHostCli builds the cli. The provider is only resolvable inside the docker network, so the cli reaches it
// through an http proxy that dials it on its host port, while the controller and the loopback callback are reached
// directly.
func newHostCli(ctx context.Context, g *env.Controller, provider *env.Oauth2) *hostCli {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	bin := filepath.Join(dir, "flock")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, ".")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := build.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "build cli: %s", out)

	proxy := httptest.NewServer(&httputil.ReverseProxy{
		// the provider builds its issuer from the Host header, it must stay the one of the network
		Rewrite:   func(r *httputil.ProxyRequest) { r.Out.Host = r.In.Host },
		Transport: provider.HTTPClient(ctx).Transport,
	})
	DeferCleanup(proxy.Close)

	home := filepath.Join(dir, "home")
	Expect(os.MkdirAll(home, 0o755)).To(Succeed())
	return &hostCli{bin: bin, home: home, host: g.Addr(ctx), proxy: proxy.URL}
}

// command gets an empty PATH so that the cli finds no browser to open and prints the login url instead
func (c *hostCli) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Dir = c.home
	cmd.Env = append(envWithoutFlock(),
		"HOME="+c.home,
		"XDG_CACHE_HOME="+filepath.Join(c.home, ".cache"),
		"PATH="+filepath.Join(c.home, "no-browser"),
		"HTTP_PROXY="+c.proxy,
		"NO_PROXY=",
		"FLOCK_CLI_CONFIG="+filepath.Join(c.home, "config.json"),
		"FLOCK_HOST="+c.host,
		"FLOCK_SKIP_SSL_VALIDATION=1",
	)
	return cmd
}

// envWithoutFlock gives the environment of the test process without its FLOCK_* variables: a developer's own would
// override the ones the cli is given here.
func envWithoutFlock() []string {
	return slices.DeleteFunc(os.Environ(), func(entry string) bool { return strings.HasPrefix(entry, "FLOCK_") })
}

var loginUrlPattern = regexp.MustCompile(`Opening (http://\S+) in your browser`)

// readLoginUrl gives the url the cli wanted to open in a browser, empty when the output ends without it
func readLoginUrl(output io.Reader) string {
	scanner := bufio.NewScanner(output)
	for scanner.Scan() {
		match := loginUrlPattern.FindStringSubmatch(scanner.Text())
		if match != nil {
			return match[1]
		}
	}
	return ""
}

func freePort() int {
	GinkgoHelper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	defer lis.Close()
	return lis.Addr().(*net.TCPAddr).Port
}
