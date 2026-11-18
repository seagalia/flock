//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	starling_route "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("WAF on HTTP routes", func() {
	const (
		vipIP = "10.250.0.210"
		base  = "http://" + vipIP + "/anything"
		// matched by the SQL injection rules of the OWASP CRS
		sqlInjection = "?id=1%27%20OR%20%271%27=%271"
		customRules  = `SecRuleEngine On
SecRule ARGS:block "@streq yes" "id:1001,phase:1,deny,status:418"`
	)

	BeforeEach(func(ctx SpecContext) {
		vhost := res.VirtualHost([]string{"*"},
			res.WithWafRoute("open", "/anything/open", "c-waf", nil),
			res.WithWafRoute("default", "/anything/default", "c-waf", &starling_route.Waf{}),
			res.WithWafRoute("detect", "/anything/detect", "c-waf",
				&starling_route.Waf{Config: &starling_route.Waf_DetectOnly{DetectOnly: true}}),
			res.WithWafRoute("custom", "/anything/custom", "c-waf",
				&starling_route.Waf{Config: &starling_route.Waf_CustomRules{CustomRules: customRules}}),
			res.WithWafRoute("large", "/anything/large", "c-waf", &starling_route.Waf{RequestBodyLimit: 256 * 1024}),
			res.WithWafRoute("small", "/anything/small", "c-waf", &starling_route.Waf{RequestBodyLimit: 16 * 1024}),
			res.WithWafRoute("partial", "/anything/partial", "c-waf",
				&starling_route.Waf{RequestBodyLimit: 16 * 1024, PartialRequestBody: true}),
		)
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-waf", []string{httpbin1IP + ":8080"}),
			res.HttpVs("vs-waf", 80, vhost),
			res.Vip("vip-waf", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-waf"),
		)
	})

	It("applies the config of each route", func(ctx SpecContext) {
		statusOf := func(url string) func() (int, error) {
			return func() (int, error) {
				status, _, err := client.HTTPGet(ctx, url)
				return status, err
			}
		}
		Eventually(statusOf(base + "/default")).Should(Equal(200))

		Expect(statusOf(base + "/open" + sqlInjection)()).To(Equal(200))
		Expect(statusOf(base + "/default" + sqlInjection)()).To(Equal(403))
		Expect(statusOf(base + "/detect" + sqlInjection)()).To(Equal(200))
		Expect(statusOf(base + "/custom" + sqlInjection)()).To(Equal(200))
		Expect(statusOf(base + "/custom?block=yes")()).To(Equal(418))
		Expect(statusOf(base + "/default?block=yes")()).To(Equal(200))
	})

	It("bounds the request body with the limit of each route", func(ctx SpecContext) {
		// 40 KiB is over the 32 KiB buffers of the listener and under the 64 KiB default of a route. 200 KiB takes the
		// CRS about 600 ms with the regular expressions of Go, so the WAF asks envoy for more than its 200 ms.
		_, err := client.Exec(ctx, "sh", "-c", `printf 'a=' > /tmp/form-40k && head -c 40960 /dev/zero | tr '\0' a >> /tmp/form-40k`)
		Expect(err).ToNot(HaveOccurred())
		_, err = client.Exec(ctx, "sh", "-c", `printf 'a=' > /tmp/form-200k && head -c 204800 /dev/zero | tr '\0' a >> /tmp/form-200k`)
		Expect(err).ToNot(HaveOccurred())
		_, err = client.Exec(ctx, "sh", "-c", `printf '{"a": "' > /tmp/json-40k && head -c 40960 /dev/zero | tr '\0' a >> /tmp/json-40k && printf '"}' >> /tmp/json-40k`)
		Expect(err).ToNot(HaveOccurred())
		postStatus := func(url, path, contentType string) func() (int, error) {
			return func() (int, error) {
				status, _, err := client.HTTPGet(ctx, url, env.WithBodyFile(path, contentType))
				return status, err
			}
		}
		const form = "application/x-www-form-urlencoded"
		Eventually(postStatus(base+"/default", "/tmp/form-40k", form)).Should(Equal(200))

		Expect(postStatus(base+"/large", "/tmp/form-200k", form)()).To(Equal(200))
		Expect(postStatus(base+"/default", "/tmp/form-200k", form)()).To(Equal(413))
		Expect(postStatus(base+"/small", "/tmp/form-40k", form)()).To(Equal(413))
		// cut at 16 KiB the JSON doesn't parse, which the parse error rule 200002 would refuse on a whole body
		Expect(postStatus(base+"/partial", "/tmp/json-40k", "application/json")()).To(Equal(200))
	})
})
