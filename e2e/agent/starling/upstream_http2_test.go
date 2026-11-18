//go:build e2e

package e2e_test

import (
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("Upstream HTTP/2 over TLS", Ordered, func() {
	const (
		httpsbinIP = "172.31.250.22"
		vipIP      = "10.250.0.170"
		sni        = "h2.e2e.local"
	)

	// envoyCounter reads a counter of the envoy admin stats, 0 when it does not exist yet
	envoyCounter := func(ctx SpecContext, name string) func() int {
		return func() int {
			out, err := main.Exec(ctx, "curl", "-sf", "http://127.0.0.1:19000/stats?filter=^"+name+"$")
			if err != nil {
				return 0
			}
			_, value, found := strings.Cut(strings.TrimSpace(out), ": ")
			if !found {
				return 0
			}
			count, _ := strconv.Atoi(value)
			return count
		}
	}

	BeforeAll(func(ctx SpecContext) {
		cert, key, err := e2eEnv.Certs.LeafCert("backend-h2", sni)
		Expect(err).ToNot(HaveOccurred())
		_, err = e2eEnv.StartHttpsHttpbin(ctx, "httpsbin-h2", httpsbinIP, cert, key)
		Expect(err).ToNot(HaveOccurred())
	})

	BeforeEach(func(ctx SpecContext) {
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-h2", []string{httpsbinIP + ":8080"}, res.WithUpstreamHttp2(e2eEnv.Certs.CAPem, sni)),
			res.HttpVs("vs-h2", 80, res.HttpRoute(res.VirtualHost([]string{"*"}), "c-h2")),
			res.Vip("vip-h2", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-h2"),
		)
	})

	It("serves a plain http/1.1 client through h2 connections to a tls backend", func(ctx SpecContext) {
		Eventually(func() (string, error) {
			return client.Hostname(ctx, "http://"+vipIP+"/hostname")
		}).Should(Equal("httpsbin-h2"))
		Expect(envoyCounter(ctx, "cluster.c-h2.upstream_cx_http2_total")()).To(BeNumerically(">=", 1))
		Expect(envoyCounter(ctx, "cluster.c-h2.upstream_cx_http1_total")()).To(BeZero())
	})
})
