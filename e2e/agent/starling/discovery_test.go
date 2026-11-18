//go:build e2e

package e2e_test

import (
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
)

// hostnamesSeen gives the backends answering a few requests in a row
func hostnamesSeen(ctx SpecContext, url string) func() []string {
	return func() []string {
		seen := make([]string, 0)
		for range 6 {
			hostname, err := client.Hostname(ctx, url)
			if err == nil && !slices.Contains(seen, hostname) {
				seen = append(seen, hostname)
			}
		}
		return seen
	}
}

var _ = Describe("Endpoints discovered with eds", Ordered, func() {
	const (
		edsIP = "172.31.250.20"
		vipIP = "10.250.0.150"
		url   = "http://" + vipIP + "/hostname"
	)
	var eds *env.StaticEds
	httpbin1 := env.EdsEndpoint{IP: httpbin1IP, Port: 8080}
	httpbin2 := env.EdsEndpoint{IP: httpbin2IP, Port: 8080}

	BeforeAll(func(ctx SpecContext) {
		var err error
		eds, err = e2eEnv.StartStaticEds(ctx, edsIP)
		Expect(err).ToNot(HaveOccurred())
	})

	BeforeEach(func(ctx SpecContext) {
		Expect(eds.SetEndpoints(ctx, "web", httpbin1)).To(Succeed())
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-eds", nil, res.WithEds("http://"+edsIP+"/web.json", "web")),
			res.HttpVs("vs-eds", 80, res.HttpRoute(res.VirtualHost([]string{"*"}), "c-eds")),
			res.Vip("vip-eds", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-eds"),
		)
	})

	It("sends traffic to the endpoints the discovery server gives", func(ctx SpecContext) {
		Eventually(func() []string { return main.EnvoyClusterHosts(ctx, "c-eds") }).Should(ConsistOf(httpbin1IP + ":8080"))
		Eventually(hostnamesSeen(ctx, url)).Should(ConsistOf("httpbin-1"))
	})

	It("follows the endpoints changed in the answer of the discovery server", func(ctx SpecContext) {
		Eventually(hostnamesSeen(ctx, url)).Should(ConsistOf("httpbin-1"))
		Expect(eds.SetEndpoints(ctx, "web", httpbin2)).To(Succeed())
		Eventually(func() []string { return main.EnvoyClusterHosts(ctx, "c-eds") }).Should(ConsistOf(httpbin2IP + ":8080"))
		Eventually(hostnamesSeen(ctx, url)).Should(ConsistOf("httpbin-2"))
		Expect(eds.SetEndpoints(ctx, "web", httpbin1, httpbin2)).To(Succeed())
		Eventually(func() []string { return main.EnvoyClusterHosts(ctx, "c-eds") }).Should(ConsistOf(httpbin1IP+":8080", httpbin2IP+":8080"))
		Eventually(hostnamesSeen(ctx, url)).Should(ConsistOf("httpbin-1", "httpbin-2"))
	})
})

var _ = Describe("Endpoints resolved with DNS", Ordered, func() {
	const (
		corednsIP = "172.31.250.21"
		vipIP     = "10.250.0.160"
		url       = "http://" + vipIP + ":8080/hostname"
		name      = "web.e2e.local"
	)
	var coredns *env.CoreDNS

	BeforeAll(func(ctx SpecContext) {
		var err error
		coredns, err = e2eEnv.StartCoreDNS(ctx, corednsIP, httpbin1IP+" "+name+"\n")
		Expect(err).ToNot(HaveOccurred())
	})

	BeforeEach(func(ctx SpecContext) {
		Expect(coredns.SetHosts(ctx, httpbin1IP+" "+name+"\n")).To(Succeed())
		e2eEnv.Controller.Push(mainInstance,
			res.Backend("c-dns", nil, res.WithDns(name, 8080, corednsIP)),
			res.TcpVs("vs-dns", 8080, "c-dns"),
			res.Vip("vip-dns", vipIP, starling_announce.StarlingAnnounce_STATIC, "vs-dns"),
		)
	})

	It("balances on the addresses of the name", func(ctx SpecContext) {
		Eventually(hostnamesSeen(ctx, url)).Should(ConsistOf("httpbin-1"))
		Expect(coredns.SetHosts(ctx, httpbin1IP+" "+name+"\n"+httpbin2IP+" "+name+"\n")).To(Succeed())
		Eventually(func() []env.IpvsDest {
			for _, svc := range main.Ipvs(ctx) {
				if svc.Addr == vipIP+":8080" {
					return svc.Dests
				}
			}
			return nil
		}).Should(HaveLen(2))
		Eventually(hostnamesSeen(ctx, url)).Should(ConsistOf("httpbin-1", "httpbin-2"))
	})

	It("drops an address removed from the name", func(ctx SpecContext) {
		Eventually(hostnamesSeen(ctx, url)).Should(ConsistOf("httpbin-1"))
		Expect(coredns.SetHosts(ctx, httpbin2IP+" "+name+"\n")).To(Succeed())
		Eventually(hostnamesSeen(ctx, url)).Should(ConsistOf("httpbin-2"))
	})
})
