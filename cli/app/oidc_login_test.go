package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/cli/internal/fakeconnect"
	"github.com/seagalia/flock/cli/internal/fakeoidc"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
)

func newFakeProvider() (*fakeoidc.Provider, *svclogin.OidcProvider) {
	GinkgoHelper()
	provider, err := fakeoidc.New()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(provider.Close)
	return provider, &svclogin.OidcProvider{
		IssuerUrl: provider.Issuer(),
		ClientId:  fakeoidc.ClientId,
		Scopes:    []string{"openid", "profile"},
	}
}

func freePort() int {
	GinkgoHelper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	port := listener.Addr().(*net.TCPAddr).Port
	Expect(listener.Close()).To(Succeed())
	return port
}

// browseWithHttpClient replaces the browser by an http client following the redirects of the login, the error the
// local server answers on a bad callback is reported by loginOidcLoopback itself.
func browseWithHttpClient() {
	previous := openBrowser
	openBrowser = func(url string) error {
		go func() {
			resp, err := http.Get(url)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}
	DeferCleanup(func() { openBrowser = previous })
}

// serveOidcController makes every client talk to a controller announcing provider
func serveOidcController(provider *svclogin.OidcProvider) {
	fake := fakeconnect.New().Handle(svcloginconnect.LoginGetServiceAuthProcedure, func(proto.Message) (proto.Message, error) {
		return &svclogin.ServiceAuth{Type: "OIDC", Oidc: provider}, nil
	})
	previous := NewClient
	NewClient = func(_ *Config, interceptors ...connect.ClientInterceptor) (*connect.Client, error) {
		return fake.Client(interceptors...), nil
	}
	DeferCleanup(func() { NewClient = previous })
}

var _ = Describe("OIDC login", func() {
	var (
		provider *fakeoidc.Provider
		oidcConf *svclogin.OidcProvider
		ctx      context.Context
	)

	BeforeEach(func() {
		clearFlockEnv()
		provider, oidcConf = newFakeProvider()
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		DeferCleanup(cancel)
	})

	It("logs a user in through the loopback with PKCE", func() {
		browseWithHttpClient()

		stored, err := loginOidcLoopback(ctx, oidcConf, freePort())

		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Kind).To(Equal("oidc_user"))
		Expect(stored.IdToken).To(Equal(provider.LastIdToken()))
		Expect(stored.RefreshToken).NotTo(BeEmpty())
		Expect(stored.Issuer).To(Equal(provider.Issuer()))
		Expect(stored.Expiry).To(BeTemporally("~", time.Now().Add(time.Hour), 5*time.Second))
		Expect(provider.Grants()).To(Equal([]string{"authorization_code"}))
	})

	It("refuses a callback with the wrong state", func() {
		browseWithHttpClient()
		provider.ForgedState = "forged"

		_, err := loginOidcLoopback(ctx, oidcConf, freePort())

		Expect(err).To(MatchError(ContainSubstring("state does not match")))
		Expect(provider.Grants()).To(BeEmpty())
	})

	It("logs a user in with the device grant", func() {
		provider.PendingPolls = 1

		stored, err := loginOidcDevice(ctx, oidcConf)

		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Kind).To(Equal("oidc_user"))
		Expect(stored.IdToken).To(Equal(provider.LastIdToken()))
		Expect(provider.Grants()).To(Equal([]string{
			"urn:ietf:params:oauth:grant-type:device_code",
			"urn:ietf:params:oauth:grant-type:device_code",
		}))
	})

	It("logs a machine in with client credentials", func() {
		stored, err := loginOidcClientCredentials(ctx, oidcConf, fakeoidc.ClientId, fakeoidc.ClientSecret)

		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Kind).To(Equal("oidc_client"))
		Expect(stored.AccessToken).NotTo(BeEmpty())
		Expect(stored.IdToken).To(BeEmpty())
		Expect(stored.Issuer).To(Equal(provider.Issuer()))
		Expect(stored.bearer().AccessToken).To(Equal(stored.AccessToken))
		Expect(stored.Expiry).To(BeTemporally("~", time.Now().Add(time.Hour), 5*time.Second))
	})

	It("takes the expiry of a machine token from its exp claim when the provider gives no expires_in", func() {
		provider.NoExpiresIn = true

		stored, err := loginOidcClientCredentials(ctx, oidcConf, fakeoidc.ClientId, fakeoidc.ClientSecret)

		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Expiry).To(BeTemporally("~", time.Now().Add(time.Hour), 5*time.Second))
		Expect(stored.bearer().Valid()).To(BeTrue())
	})

	It("stores the session in a directory and a file only the user can read", func() {
		cacheDir := isolateSessions()
		cnf := &Config{Host: "flock.local:443", ClientId: fakeoidc.ClientId, ClientSecret: fakeoidc.ClientSecret}

		Expect(LoginOidc(ctx, cnf, oidcConf, false, 0)).To(Succeed())

		sessionsDir := filepath.Join(cacheDir, "flock", "sessions")
		dirInfo, err := os.Stat(sessionsDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(dirInfo.Mode().Perm()).To(Equal(os.FileMode(0700)))
		path, err := sessionPath(cnf.Host)
		Expect(err).NotTo(HaveOccurred())
		fileInfo, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(filepath.Dir(path)).To(Equal(sessionsDir))
		Expect(fileInfo.Mode().Perm()).To(Equal(os.FileMode(0600)))
	})

	It("tightens a session directory and file that already exist with looser permissions", func() {
		cacheDir := isolateSessions()
		sessionsDir := filepath.Join(cacheDir, "flock", "sessions")
		Expect(os.MkdirAll(sessionsDir, 0755)).To(Succeed())
		Expect(os.Chmod(sessionsDir, 0755)).To(Succeed())
		path, err := sessionPath("flock.local:443")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(path, []byte("{}"), 0644)).To(Succeed())

		Expect(writeStoredToken(path, &storedToken{Kind: "flock", AccessToken: "token"})).To(Succeed())

		dirInfo, err := os.Stat(sessionsDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(dirInfo.Mode().Perm()).To(Equal(os.FileMode(0700)))
		fileInfo, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(fileInfo.Mode().Perm()).To(Equal(os.FileMode(0600)))
	})

	It("gives up on a provider that does not answer", func() {
		previous := oidcHttpTimeout
		oidcHttpTimeout = 100 * time.Millisecond
		DeferCleanup(func() { oidcHttpTimeout = previous })
		hanging := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		DeferCleanup(hanging.Close)
		cnf := &Config{Host: "flock.local:443", ClientId: fakeoidc.ClientId, ClientSecret: fakeoidc.ClientSecret}
		isolateSessions()

		err := LoginOidc(ctx, cnf, &svclogin.OidcProvider{IssuerUrl: hanging.URL, ClientId: fakeoidc.ClientId}, false, 0)

		Expect(err).To(MatchError(ContainSubstring("Client.Timeout exceeded")))
	})

	It("refuses wrong client credentials", func() {
		_, err := loginOidcClientCredentials(ctx, oidcConf, fakeoidc.ClientId, "wrong")

		Expect(err).To(MatchError(ContainSubstring("invalid_client")))
	})
})

var _ = Describe("newTokenSource", func() {
	var (
		provider *fakeoidc.Provider
		oidcConf *svclogin.OidcProvider
		cnf      *Config
		path     string
	)

	BeforeEach(func() {
		clearFlockEnv()
		provider, oidcConf = newFakeProvider()
		serveOidcController(oidcConf)
		cnf = &Config{Host: "flock.local:443"}
		path = filepath.Join(GinkgoT().TempDir(), "flock.token")
	})

	It("refreshes an expired ID token", func() {
		expired := provider.SignIdToken(time.Now().Add(-time.Minute))
		Expect(writeStoredToken(path, &storedToken{
			Kind: "oidc_user", AccessToken: "access", RefreshToken: "refresh", IdToken: expired,
			Issuer: provider.Issuer(), Expiry: time.Now().Add(-time.Minute),
		})).To(Succeed())

		source, err := newTokenSource(context.Background(), cnf, path)
		Expect(err).NotTo(HaveOccurred())
		tok, err := source.Token()

		Expect(err).NotTo(HaveOccurred())
		Expect(provider.Grants()).To(Equal([]string{"refresh_token"}))
		Expect(tok.AccessToken).To(Equal(provider.LastIdToken()))
		Expect(tok.AccessToken).NotTo(Equal(expired))
		stored, err := readStoredToken(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.IdToken).To(Equal(provider.LastIdToken()))
		Expect(stored.Expiry).To(BeTemporally(">", time.Now()))
	})

	It("asks to log in again without refreshing when the controller announces another provider", func() {
		Expect(writeStoredToken(path, &storedToken{
			Kind: "oidc_user", AccessToken: "access", RefreshToken: "refresh",
			IdToken: provider.SignIdToken(time.Now().Add(-time.Minute)), Issuer: "https://old-provider.example.com",
			Expiry: time.Now().Add(-time.Minute),
		})).To(Succeed())

		source, err := newTokenSource(context.Background(), cnf, path)
		Expect(err).NotTo(HaveOccurred())
		_, err = source.Token()

		Expect(err).To(MatchError("the controller changed its OIDC provider, run flock login again"))
		Expect(provider.Grants()).To(BeEmpty())
		stored, err := readStoredToken(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.RefreshToken).To(Equal("refresh"))
	})

	It("gives a valid ID token without calling the provider", func() {
		idToken := provider.SignIdToken(time.Now().Add(time.Hour))
		Expect(writeStoredToken(path, &storedToken{
			Kind: "oidc_user", AccessToken: "access", IdToken: idToken, Expiry: time.Now().Add(time.Hour),
		})).To(Succeed())

		source, err := newTokenSource(context.Background(), cnf, path)
		Expect(err).NotTo(HaveOccurred())
		tok, err := source.Token()

		Expect(err).NotTo(HaveOccurred())
		Expect(tok.AccessToken).To(Equal(idToken))
		Expect(provider.Grants()).To(BeEmpty())
	})

	It("asks to log in again when the refresh gives no ID token", func() {
		provider.NoIdTokenOnRefresh = true
		Expect(writeStoredToken(path, &storedToken{
			Kind: "oidc_user", AccessToken: "access", RefreshToken: "refresh", Issuer: provider.Issuer(),
			IdToken: provider.SignIdToken(time.Now().Add(-time.Minute)), Expiry: time.Now().Add(-time.Minute),
		})).To(Succeed())

		source, err := newTokenSource(context.Background(), cnf, path)
		Expect(err).NotTo(HaveOccurred())
		_, err = source.Token()

		Expect(err).To(MatchError("the provider did not renew the ID token, run flock login again"))
	})

	It("renews an expired machine token with the client credentials of the environment", func() {
		Expect(writeStoredToken(path, &storedToken{
			Kind: "oidc_client", AccessToken: "old", Expiry: time.Now().Add(-time.Minute),
		})).To(Succeed())
		cnf.ClientId = fakeoidc.ClientId
		cnf.ClientSecret = fakeoidc.ClientSecret

		source, err := newTokenSource(context.Background(), cnf, path)
		Expect(err).NotTo(HaveOccurred())
		tok, err := source.Token()

		Expect(err).NotTo(HaveOccurred())
		Expect(tok.AccessToken).NotTo(Equal("old"))
		Expect(provider.Grants()).To(Equal([]string{"client_credentials"}))
	})

	It("asks to log in again when a machine token expired without client credentials", func() {
		Expect(writeStoredToken(path, &storedToken{
			Kind: "oidc_client", AccessToken: "old", Expiry: time.Now().Add(-time.Minute),
		})).To(Succeed())

		source, err := newTokenSource(context.Background(), cnf, path)
		Expect(err).NotTo(HaveOccurred())
		_, err = source.Token()

		Expect(err).To(MatchError("your session expired, run flock login again"))
	})

	It("sends FLOCK_TOKEN as is and touches no file", func() {
		cnf.Token = "tok"
		fake := fakeconnect.New().Handle(svcloginconnect.LoginMeProcedure, func(proto.Message) (proto.Message, error) {
			return &svclogin.MeResponse{}, nil
		})
		NewClient = func(_ *Config, interceptors ...connect.ClientInterceptor) (*connect.Client, error) {
			return fake.Client(interceptors...), nil
		}
		cacheDir := isolateSessions()

		client, _, err := MakeClient(context.Background(), cnf, false)
		Expect(err).NotTo(HaveOccurred())
		_, err = svcloginconnect.NewLoginClient(client).Me(context.Background(), &emptypb.Empty{})

		Expect(err).NotTo(HaveOccurred())
		Expect(fake.RequestHeaders(svcloginconnect.LoginMeProcedure)[0].Get("authorization")).To(Equal("Bearer tok"))
		Expect(fake.Calls()).To(Equal([]string{svcloginconnect.LoginMeProcedure}))
		Expect(filepath.Join(cacheDir, "flock")).NotTo(BeADirectory())
	})

	It("prefers FLOCK_TOKEN over a stored session", func() {
		Expect(writeStoredToken(path, &storedToken{
			Kind: "flock", AccessToken: "stored", Expiry: time.Now().Add(time.Hour),
		})).To(Succeed())
		cnf.Token = "tok"

		source, err := newTokenSource(context.Background(), cnf, path)
		Expect(err).NotTo(HaveOccurred())
		tok, err := source.Token()

		Expect(err).NotTo(HaveOccurred())
		Expect(tok.AccessToken).To(Equal("tok"))
	})
})
