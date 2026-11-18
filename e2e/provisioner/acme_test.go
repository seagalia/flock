//go:build e2e

package provisioner_test

import (
	"crypto/x509"
	"encoding/pem"
	"maps"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

const (
	mdSecretRef   = "flock.acme.secret.ref"
	mdAttached    = "flock.acme.secret.attached"
	mdCertRef     = "flock.acme.cert.ref"
	mdAccountKey  = "flock.acme.key_pem"
	mdAccountInfo = "flock.acme.registration"
)

// exposeCertificate creates a certificate for the acme provisioner, with extraMetadata beside the acme keys, and serves
// it on a virtual server of a VIP of the instance: the controller sends the provisioner the certificates of its
// instance only
func exposeCertificate(
	ctx SpecContext, name, domain, vipIP string, extraMetadata map[string]string,
) (certUuid, vipUuid string) {
	GinkgoHelper()
	metadata := map[string]string{
		"flock.provisioner.acme": "true",
		"flock.acme.email":       "e2e@flock.test",
	}
	maps.Copy(metadata, extraMetadata)
	certResp, err := api.Certificate.CreateCertificate(ctx, &svccert.CreateCertificateRequest{Certificate: &cert.Certificate{
		Name:        name,
		DomainNames: []string{domain},
		Metadata:    metadata,
	}})
	Expect(err).ToNot(HaveOccurred())
	certUuid = certResp.GetCertificate().GetUUID()

	vsResp, err := api.VS.CreateVS(ctx, &svcvs.CreateVSRequest{Vs: &vs.VirtualServer{
		Name: name,
		Port: 443,
		RouteConfig: &vs.VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &vs.HttpRoutesConfig{
			VirtualHosts: []*route.VirtualHost{{
				Domains:    []string{domain},
				TlsOptions: []*core.TLS{{CertificatesUuid: []string{certUuid}}},
				Routes: []*route.Route{{
					Name:  "default",
					Match: &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
					Action: &route.Route_DirectResponse{DirectResponse: &route.DirectResponseAction{
						Status:    200,
						Specifier: &route.DirectResponseAction_InlineString{InlineString: "ok"},
					}},
				}},
			}},
		}},
	}})
	Expect(err).ToNot(HaveOccurred())

	vipResp, err := api.VIP.CreateVIP(ctx, &svcvip.CreateVIPRequest{Vip: &vip.VIP{
		Name:               name,
		Ip:                 vipIP,
		PrefixLength:       32,
		VirtualServersUuid: []string{vsResp.GetVs().GetUUID()},
	}})
	Expect(err).ToNot(HaveOccurred())
	vipUuid = vipResp.GetVip().GetUUID()

	_, err = api.Instance.AddVipToInstance(ctx, &svcinstance.AddVipToInstanceRequest{UUID: instanceUuid, VipUuid: vipUuid})
	Expect(err).ToNot(HaveOccurred())
	return certUuid, vipUuid
}

// expectIssuedByPebble waits for the provisioner to write back a certificate pebble signed for domain. The controller
// never gives the private key back, the chain is what a spec can check.
func expectIssuedByPebble(ctx SpecContext, certUuid, domain string) *cert.Certificate {
	GinkgoHelper()
	var issued *cert.Certificate
	Eventually(func(g Gomega) {
		resp, err := api.Certificate.ReadCertificate(ctx, &svccore.UuidRequest{UUID: certUuid})
		g.Expect(err).ToNot(HaveOccurred())
		issued = resp.GetCertificate()
		g.Expect(issued.GetCertChain()).ToNot(BeEmpty())
	}).Should(Succeed())

	block, _ := pem.Decode([]byte(issued.GetCertChain()))
	Expect(block).ToNot(BeNil())
	leaf, err := x509.ParseCertificate(block.Bytes)
	Expect(err).ToNot(HaveOccurred())
	Expect(leaf.Issuer.CommonName).To(HavePrefix("Pebble Intermediate CA"))
	Expect(leaf.DNSNames).To(ConsistOf(domain))
	return issued
}

func readSecretMetadata(ctx SpecContext, secretUuid string) (map[string]string, error) {
	resp, err := api.Secret.ReadSecret(ctx, &svccore.UuidRequest{UUID: secretUuid})
	if err != nil {
		return nil, err
	}
	return resp.GetSecret().GetMetadata(), nil
}

var _ = Describe("The acme provisioner", Ordered, func() {
	var (
		certUuid   string
		vipUuid    string
		secretUuid string
	)

	BeforeAll(func(ctx SpecContext) {
		startProvisioner(ctx, false)
	})

	It("obtains the certificate of a virtual server of its instance with http-01", func(ctx SpecContext) {
		certUuid, vipUuid = exposeCertificate(ctx, "http-shop", "shop.flock.test", "10.253.0.10", nil)

		issued := expectIssuedByPebble(ctx, certUuid, "shop.flock.test")

		md := issued.GetMetadata()
		Expect(md).To(HaveKeyWithValue(helpers.MetadataProvisionBy, provisionerUuid))
		Expect(md).To(HaveKeyWithValue("flock.acme.email", "e2e@flock.test"))
		secretUuid = md[mdSecretRef]
		Expect(secretUuid).ToNot(BeEmpty())
		// the account of the CA stays in the secret, the certificate only points at it
		Expect(md).To(HaveKeyWithValue(mdAccountKey, "secret://"+secretUuid+"/"+mdAccountKey))
		Expect(md).To(HaveKeyWithValue(mdAccountInfo, "secret://"+secretUuid+"/"+mdAccountInfo))
	})

	It("is declared to the controller as a provisioner", func(ctx SpecContext) {
		resp, err := api.Lookup.ReadAgent(ctx, &svclookup.ReadAgentRequest{AgentUuid: provisionerUuid})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.GetAgent().GetName()).To(Equal(provisionerName))
		Expect(resp.GetAgent().GetMode()).To(Equal(agent.Agent_PROVISIONER))
		Expect(resp.GetInstanceUuid()).To(Equal(instanceUuid))
	})

	It("answers the status of the certificate through the controller", func(ctx SpecContext) {
		Eventually(func(g Gomega) {
			resp, err := api.Lookup.GetStatus(ctx, &svclookup.GetStatusRequest{
				InstanceUuid: instanceUuid,
				AgentUuid:    provisionerUuid,
				TypeUrl:      helpers.TypeUrl[*cert.Certificate](),
				Identifier:   certUuid,
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetAgentUuid()).To(Equal(provisionerUuid))
			g.Expect(resp.GetState()).To(Equal(svclookup.StatusResponse_OK), resp.GetError())
			held := &cert.Certificate{}
			g.Expect(resp.GetPreviousResource().UnmarshalTo(held)).To(Succeed())
			g.Expect(held.GetUUID()).To(Equal(certUuid))
		}).Should(Succeed())
	})

	It("keeps the account of the CA in a secret attached to the certificate", func(ctx SpecContext) {
		// ReadSecret blanks the values, the admin reveals them
		resp, err := api.Secret.RevealSecret(ctx, &svccore.UuidRequest{UUID: secretUuid})
		Expect(err).ToNot(HaveOccurred())
		md := resp.GetSecret().GetMetadata()
		Expect(md).To(HaveKeyWithValue(mdCertRef, certUuid))
		Expect(md).To(HaveKeyWithValue(mdAttached, "true"))
		Expect(resp.GetSecret().GetData()).To(HaveKey(mdAccountKey))
		accountKey := resp.GetSecret().GetData()[mdAccountKey]
		Expect(strings.TrimSpace(accountKey)).To(HavePrefix("-----BEGIN"))
	})

	It("detaches the secret once no virtual server of the instance serves the certificate", func(ctx SpecContext) {
		_, err := api.Instance.DeleteVipFromInstance(ctx, &svcinstance.DeleteVipFromInstanceRequest{
			UUID: instanceUuid, VipUuid: vipUuid,
		})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() (map[string]string, error) {
			return readSecretMetadata(ctx, secretUuid)
		}).Should(HaveKeyWithValue(mdAttached, "false"))
	})

	It("obtains a certificate with tls-alpn-01", func(ctx SpecContext) {
		startProvisioner(ctx, true)

		tlsCertUuid, _ := exposeCertificate(ctx, "tls-shop", "tls.flock.test", "10.253.0.11", nil)

		expectIssuedByPebble(ctx, tlsCertUuid, "tls.flock.test")
		// pebble logs only its http-01 attempts, lego logs the solver it used
		Expect(provisioner.Logs(ctx)).To(ContainSubstring("domain=tls.flock.test type=tls-alpn-01"))
	})
})
