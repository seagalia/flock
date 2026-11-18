//go:build e2e

package provisioner_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"

	"github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/sdk/helpers"
)

const (
	dnsDomain         = "dns.flock.test"
	dnsSecondDomain   = "www.dns.flock.test"
	mdHttpreqEndpoint = "flock.acme.httpreq.endpoint"
	obtainLog         = "Obtaining bundled SAN certificate"
	leaseRetrySlack   = 15 * time.Second
	// the shortest the controller grants, it bounds how long the replica refused the lease waits before it retries
	replicaLeaseTTL = 10 * time.Second
	// the challenge types as lego logs them
	http01    = "http-01"
	tlsAlpn01 = "tls-alpn-01"
	dns01     = "dns-01"
	// each certificate goes to replica-1 about one time in two, so ten miss it about once in 1024 runs
	maxSharedOrders = 10
)

// startReplica runs a provisioner answering challenge. Replicas of one instance share their http-01 and tls-alpn-01
// answers through the controller, so each of them listens on the challenge ports and answers the orders of the other.
func startReplica(ctx context.Context, name, ip, challenge string) {
	GinkgoHelper()
	replica, err := e2eEnv.StartProvisioner(ctx, env.ProvisionerOptions{
		Name:           name,
		IP:             ip,
		Instance:       instanceUuid,
		ControllerAddr: controller.Target(),
		Pebble:         pebble,
		TlsAlpn01:      challenge == tlsAlpn01,
		Dns01:          challenge == dns01,
		ChallTestSrvIP: challtestsrvIP,
		LeaseTTL:       replicaLeaseTTL,
	})
	Expect(err).ToNot(HaveOccurred())
	replicas[env.AgentUuid(name)] = replica
}

// startReplicas replaces the single provisioner with two replicas answering challenge, stopped once the calling
// container ends
func startReplicas(ctx SpecContext, challenge string) {
	GinkgoHelper()
	// the single provisioner runs as provisioner-1 too, the controller would see two of them
	if provisioner != nil {
		Expect(provisioner.Container.Terminate(ctx)).To(Succeed())
		provisioner = nil
	}
	// registered first so the replica already started is stopped when the start of the other one fails
	DeferCleanup(func(ctx SpecContext) {
		for agentUuid := range replicas {
			stopReplica(ctx, agentUuid)
		}
	})
	startReplica(ctx, provisionerName, provisionerIP, challenge)
	startReplica(ctx, replicaName, replicaIP, challenge)
}

// expectReplicaOneAnswered creates certificates one at a time until replica-1 orders one, while pebble reaches
// replica-2 only, so that order succeeds only when replica-2 answers the challenge replica-1 wrote. The lease race
// picks the holder of each certificate, firstVipHost spreads their VIPs apart from those of the other specs.
func expectReplicaOneAnswered(ctx SpecContext, challenge string, firstVipHost int) {
	GinkgoHelper()
	for i := range maxSharedOrders {
		name := fmt.Sprintf("%s-replicas-%d", challenge, i)
		domain := name + ".flock.test"
		Expect(challtestsrv.ResolveA(ctx, domain, replicaIP)).To(Succeed())
		certUuid, _ := exposeCertificate(ctx, name, domain, fmt.Sprintf("10.253.0.%d", firstVipHost+i), nil)

		issued := expectIssuedByPebble(ctx, certUuid, domain)

		holderUuid := issued.GetMetadata()[helpers.MetadataProvisionBy]
		Expect(replicas).To(HaveKey(holderUuid))
		Expect(replicas[holderUuid].Logs(ctx)).To(ContainSubstring("domain=" + domain + " type=" + challenge))
		holderName := replicaName
		if holderUuid == provisionerUuid {
			holderName = provisionerName
		}
		AddReportEntry("holder of "+domain, holderName)
		if holderUuid == provisionerUuid {
			return
		}
	}
	Fail(fmt.Sprintf("%s ordered none of the %d certificates, the lease race always went to %s",
		provisionerName, maxSharedOrders, replicaName))
}

// stopReplica keeps the logs of the replica, the dump on failure only reads the replicas still running
func stopReplica(ctx context.Context, agentUuid string) {
	GinkgoHelper()
	logs := replicas[agentUuid].Logs(ctx)
	Expect(replicas[agentUuid].Container.Terminate(ctx)).To(Succeed())
	delete(replicas, agentUuid)
	AddReportEntry("stopped replica "+agentUuid+" logs", logs, types.ReportEntryVisibilityFailureOrVerbose)
}

// countObtains counts the orders lego started for domain across the replicas still running
func countObtains(ctx context.Context, domain string) int {
	count := 0
	for _, replica := range replicas {
		for line := range strings.Lines(replica.Logs(ctx)) {
			if strings.Contains(line, obtainLog) && strings.Contains(line, domain) {
				count++
			}
		}
	}
	return count
}

var _ = Describe("Two acme provisioner replicas of one instance", Ordered, func() {
	var (
		certUuid   string
		holderUuid string
	)

	BeforeAll(func(ctx SpecContext) {
		startReplicas(ctx, dns01)
	})

	It("obtains a certificate with dns-01 from one replica", func(ctx SpecContext) {
		certUuid, _ = exposeCertificate(ctx, "dns-shop", dnsDomain, "10.253.0.12", map[string]string{
			mdHttpreqEndpoint: env.TxtShimEndpoint,
		})

		issued := expectIssuedByPebble(ctx, certUuid, dnsDomain)

		holderUuid = issued.GetMetadata()[helpers.MetadataProvisionBy]
		Expect(replicas).To(HaveKey(holderUuid))
		Expect(replicas[holderUuid].Logs(ctx)).To(ContainSubstring("domain=" + dnsDomain + " type=" + dns01))
	})

	It("orders the certificate once across the replicas", func(ctx SpecContext) {
		// the replica refused the lease retries when the lease it saw ends, a second order would start by then
		Consistently(func() int {
			return countObtains(ctx, dnsDomain)
		}).WithTimeout(replicaLeaseTTL + leaseRetrySlack).WithPolling(time.Second).Should(Equal(1))
	})

	It("renews the certificate of a replica gone from the other replica", func(ctx SpecContext) {
		stopReplica(ctx, holderUuid)
		var takerUuid string
		for agentUuid := range replicas {
			takerUuid = agentUuid
		}

		// a new domain makes the certificate due at once, without waiting for the end of its lifetime
		resp, err := api.Certificate.ReadCertificate(ctx, &svccore.UuidRequest{UUID: certUuid})
		Expect(err).ToNot(HaveOccurred())
		withSecondDomain := resp.GetCertificate()
		withSecondDomain.DomainNames = append(withSecondDomain.DomainNames, dnsSecondDomain)
		_, err = api.Certificate.UpdateCertificate(ctx, &svccert.UpdateCertificateRequest{
			UUID:        certUuid,
			Certificate: withSecondDomain,
		})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			resp, err := api.Certificate.ReadCertificate(ctx, &svccore.UuidRequest{UUID: certUuid})
			g.Expect(err).ToNot(HaveOccurred())
			renewed := resp.GetCertificate()
			g.Expect(renewed.GetMetadata()).To(HaveKeyWithValue(helpers.MetadataProvisionBy, takerUuid))
			block, _ := pem.Decode([]byte(renewed.GetCertChain()))
			g.Expect(block).ToNot(BeNil())
			leaf, err := x509.ParseCertificate(block.Bytes)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(leaf.Issuer.CommonName).To(HavePrefix("Pebble Intermediate CA"))
			g.Expect(leaf.DNSNames).To(ConsistOf(dnsDomain, dnsSecondDomain))
		}).Should(Succeed())
	})
})

var _ = Describe("Two acme provisioner replicas answering http-01", Ordered, func() {
	BeforeAll(func(ctx SpecContext) {
		startReplicas(ctx, http01)
	})

	It("issues http-01 certificates whichever replica the CA reaches", func(ctx SpecContext) {
		expectReplicaOneAnswered(ctx, http01, 20)
	})
})

var _ = Describe("Two acme provisioner replicas answering tls-alpn-01", Ordered, func() {
	BeforeAll(func(ctx SpecContext) {
		startReplicas(ctx, tlsAlpn01)
	})

	It("issues tls-alpn-01 certificates whichever replica the CA reaches", func(ctx SpecContext) {
		expectReplicaOneAnswered(ctx, tlsAlpn01, 40)
	})
})
