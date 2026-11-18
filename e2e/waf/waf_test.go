//go:build e2e

package waf_test

import (
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const sqlInjectionQuery = "/?id=1%27%20OR%20%271%27%3D%271"

var formHeader = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

var _ = Describe("WAF behind envoy", func() {
	Context("with the default CRS WAF", func() {
		It("lets clean requests through", func() {
			Expect(call("", http.MethodGet, "/", "", nil)).To(Equal(http.StatusOK))
			Expect(call("", http.MethodPost, "/", "name=flock", formHeader)).To(Equal(http.StatusOK))
		})

		It("blocks an SQL injection in a form body", func() {
			Expect(call("", http.MethodPost, "/", "id=1' OR '1'='1", formHeader)).To(Equal(http.StatusForbidden))
		})

		It("blocks a response leaking a database error", func() {
			Expect(call("", http.MethodGet, "/leak", "", nil)).To(Equal(http.StatusForbidden))
		})

		It("logs the blocked request with its rules", func(ctx SpecContext) {
			Expect(call("", http.MethodGet, sqlInjectionQuery, "", nil)).To(Equal(http.StatusForbidden))
			Eventually(func() string { return containerLogs(ctx) }).Should(And(
				ContainSubstring(`"message":"WAF rules matched"`),
				ContainSubstring(`"blocked":true`),
			))
		})
	})

	Context("with a WAF config registered by the agent", func() {
		It("fails closed until the config is registered", func(ctx SpecContext) {
			Expect(call("detect", http.MethodGet, "/", "", nil)).To(Equal(http.StatusInternalServerError))

			Expect(wafctl(ctx, "register", "detect", "--detect-only")).To(Succeed())
			Expect(call("detect", http.MethodGet, sqlInjectionQuery, "", nil)).To(Equal(http.StatusOK))

			Expect(wafctl(ctx, "unregister", "detect")).To(Succeed())
			Expect(call("detect", http.MethodGet, "/", "", nil)).To(Equal(http.StatusInternalServerError))
		})

		It("applies custom rules instead of the CRS", func(ctx SpecContext) {
			rules := `SecRuleEngine On
SecRule REQUEST_HEADERS:X-Block "@streq yes" "id:1001,phase:1,deny,status:403"
SecRule REQUEST_URI "@beginsWith /moved" "id:1002,phase:1,redirect:https://example.com/new"`
			Expect(wafctl(ctx, "register", "custom", "--rules", rules)).To(Succeed())
			DeferCleanup(func(ctx SpecContext) error {
				return wafctl(ctx, "unregister", "custom")
			})

			Expect(call("custom", http.MethodGet, "/", "", map[string]string{"X-Block": "yes"})).To(Equal(http.StatusForbidden))
			Expect(call("custom", http.MethodGet, sqlInjectionQuery, "", nil)).To(Equal(http.StatusOK))

			noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}
			resp, err := noRedirect.Get(listenerURLs["custom"] + "/moved")
			Expect(err).ToNot(HaveOccurred())
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusFound))
			Expect(resp.Header.Get("Location")).To(Equal("https://example.com/new"))
		})
	})
})
