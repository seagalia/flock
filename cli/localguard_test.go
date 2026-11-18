package cli

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("local guard", func() {
	var (
		guard  *localGuard
		server *httptest.Server
	)

	BeforeEach(func() {
		guard = newLocalGuard("flock_test_")
		page := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("page")) })
		server = httptest.NewServer(guard.Wrap(page))
		DeferCleanup(server.Close)
		guard.SetHost(strings.TrimPrefix(server.URL, "http://"))
	})

	serverPort := func() string {
		_, port, err := net.SplitHostPort(guard.host)
		Expect(err).NotTo(HaveOccurred())
		return port
	}

	withCookie := func(req *http.Request) *http.Request {
		req.AddCookie(&http.Cookie{Name: guard.cookieName(), Value: guard.secret})
		return req
	}

	// noRedirect keeps the answer of the guard instead of following it.
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	get := func(req *http.Request) *http.Response {
		GinkgoHelper()
		resp, err := noRedirect.Do(req)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(resp.Body.Close)
		return resp
	}

	newGet := func(path string) *http.Request {
		GinkgoHelper()
		req, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
		Expect(err).NotTo(HaveOccurred())
		return req
	}

	It("refuses a request without the cookie", func() {
		resp := get(newGet("/dashboard/"))

		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	})

	It("refuses another Host", func() {
		req := withCookie(newGet("/dashboard/"))
		req.Host = "evil.example.com"

		resp := get(req)

		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
	})

	It("refuses a wrong secret", func() {
		resp := get(newGet("/dashboard/?k=not-the-secret"))

		Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		Expect(resp.Cookies()).To(BeEmpty())
	})

	It("trades the secret for a cookie, then lets the cookie through", func() {
		resp := get(newGet("/dashboard/?k=" + guard.secret))

		Expect(resp.StatusCode).To(Equal(http.StatusSeeOther))
		Expect(resp.Header.Get("Location")).To(Equal("/dashboard/"))
		Expect(resp.Cookies()).To(HaveLen(1))
		cookie := resp.Cookies()[0]
		Expect(cookie.Name).To(Equal("flock_test_" + serverPort()))
		Expect(cookie.Value).To(Equal(guard.secret))
		Expect(cookie.HttpOnly).To(BeTrue())
		Expect(cookie.SameSite).To(Equal(http.SameSiteStrictMode))
		Expect(cookie.Path).To(Equal("/"))

		allowed := get(withCookie(newGet("/dashboard/")))
		Expect(allowed.StatusCode).To(Equal(http.StatusOK))
	})

	It("redirects only to a path of the page", func() {
		resp := get(newGet("//evil.example.com/x?k=" + guard.secret))

		Expect(resp.StatusCode).To(Equal(http.StatusSeeOther))
		Expect(resp.Header.Get("Location")).To(Equal("/dashboard/"))
	})

	It("names its cookie after its port, so two guards don't share it", func() {
		other := newLocalGuard("flock_test_")
		other.SetHost("127.0.0.1:1")

		Expect(guard.cookieName()).To(Equal("flock_test_" + serverPort()))
		Expect(other.cookieName()).To(Equal("flock_test_1"))
	})

	It("makes a secret of 32 random bytes per guard", func() {
		other := newLocalGuard("flock_test_")

		Expect(guard.secret).To(MatchRegexp(`^[0-9a-f]{64}$`))
		Expect(other.secret).NotTo(Equal(guard.secret))
	})

	It("builds the url to open with the secret", func() {
		Expect(guard.URL("/dashboard/")).To(Equal("http://" + guard.host + "/dashboard/?k=" + guard.secret))
	})
})
