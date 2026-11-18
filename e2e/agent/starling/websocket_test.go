//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

var _ = Describe("WebSocket through Envoy", func() {
	const (
		vipIP    = "10.250.0.120"
		domain   = "ws.e2e.local"
		certUuid = "cert-ws"
	)

	BeforeEach(func(ctx SpecContext) {
		certPem, keyPem, err := e2eEnv.Certs.LeafCert("ws", domain)
		Expect(err).ToNot(HaveOccurred())
		e2eEnv.Controller.Push(mainInstance,
			res.Certificate(certUuid, certPem, keyPem),
			res.Backend("c-ws", []string{httpbin1IP + ":8080"}),
			res.HttpVs("vs-ws", 80, res.HttpRoute(res.VirtualHost([]string{domain}), "c-ws")),
			res.HttpVs("vs-wss", 443, res.HttpRoute(res.VirtualHost([]string{domain}, res.WithTls(certUuid, domain)), "c-ws")),
			res.Vip("vip-ws", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-ws", "vs-wss"),
		)
	})

	It("upgrades on an http virtual server", func(ctx SpecContext) {
		Eventually(func() (string, error) {
			return client.WebsocketEcho(ctx, "ws://"+domain+"/websocket/echo", "hello", env.WithResolve(domain, 80, vipIP))
		}).Should(Equal("hello"))
	})

	It("upgrades on an https virtual server", func(ctx SpecContext) {
		Eventually(func() (string, error) {
			return client.WebsocketEcho(ctx, "wss://"+domain+"/websocket/echo", "hello",
				env.WithResolve(domain, 443, vipIP), env.WithCA())
		}).Should(Equal("hello"))
	})
})
