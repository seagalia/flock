package factories

import (
	"context"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/ovh"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/acme/params"
)

var _ = Describe("LegoFactory.makeDnsProvider", func() {
	factory := &LegoFactory{}

	It("makes the provider named in the metadata, with its params", func() {
		provider, err := factory.makeDnsProvider(&params.CertRequest{DnsProvider: map[string]any{
			"flock.acme.ovh.api_endpoint":       "ovh-eu",
			"flock.acme.ovh.application_key":    "ak",
			"flock.acme.ovh.application_secret": "as",
			"flock.acme.ovh.consumer_key":       "ck",
			"flock.provisioner.acme":            "true",
		}})

		Expect(err).NotTo(HaveOccurred())
		Expect(provider).To(BeAssignableToTypeOf(&ovh.DNSProvider{}))
	})

	It("makes no provider when the metadata names none, the CA then validates over http-01 or tls-alpn-01", func() {
		provider, err := factory.makeDnsProvider(&params.CertRequest{DnsProvider: map[string]any{
			"flock.provisioner.acme": "true",
		}})

		Expect(err).NotTo(HaveOccurred())
		Expect(provider).To(BeNil())
	})

	It("ignores the challenge reference and the secret keys the certificate holds beside the provider", func() {
		provider, err := factory.makeDnsProvider(&params.CertRequest{DnsProvider: map[string]any{
			"flock.acme.ovh.api_endpoint":       "ovh-eu",
			"flock.acme.ovh.application_key":    "ak",
			"flock.acme.ovh.application_secret": "as",
			"flock.acme.ovh.consumer_key":       "ck",
			"flock.acme.challenges.ref":         "challenges",
			"flock.acme.secret.ref":             "secret-1",
			"flock.acme.secret.attached":        "true",
		}})

		Expect(err).NotTo(HaveOccurred())
		Expect(provider).To(BeAssignableToTypeOf(&ovh.DNSProvider{}))
	})

	It("fails on a param of the provider that doesn't parse", func() {
		_, err := factory.makeDnsProvider(&params.CertRequest{DnsProvider: map[string]any{
			"flock.acme.ovh.application_key":     "ak",
			"flock.acme.ovh.propagation_timeout": "soon",
		}})

		Expect(err).To(MatchError(ContainSubstring("propagation_timeout")))
	})
})

var _ = Describe("LegoFactory.challengeProviders", func() {
	solver := challenge.Provider(nil)

	BeforeEach(func() {
		solver = &stubSolver{}
	})

	It("sets the solver of the order for the enabled challenges only", func() {
		factory := &LegoFactory{http01Enabled: true}

		http01, tlsAlpn01 := factory.challengeProviders(solver)

		Expect(http01).To(BeIdenticalTo(solver))
		Expect(tlsAlpn01).To(BeNil())
	})

	It("sets the solver on both challenges when both are enabled", func() {
		factory := &LegoFactory{http01Enabled: true, tlsAlpn01Enabled: true}

		http01, tlsAlpn01 := factory.challengeProviders(solver)

		Expect(http01).To(BeIdenticalTo(solver))
		Expect(tlsAlpn01).To(BeIdenticalTo(solver))
	})

	It("sets nothing for a nil solver", func() {
		factory := &LegoFactory{http01Enabled: true, tlsAlpn01Enabled: true}

		http01, tlsAlpn01 := factory.challengeProviders(nil)

		Expect(http01).To(BeNil())
		Expect(tlsAlpn01).To(BeNil())
	})
})

type stubSolver struct{}

func (*stubSolver) Present(context.Context, string, string, string) error { return nil }
func (*stubSolver) CleanUp(context.Context, string, string, string) error { return nil }
