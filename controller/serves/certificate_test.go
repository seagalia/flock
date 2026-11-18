package serves_test

import (
	"context"

	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	corev1 "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
)

func generateCertPair() (string, string) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test.local"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).ToNot(HaveOccurred())
	keyDer, err := x509.MarshalECPrivateKey(key)
	Expect(err).ToNot(HaveOccurred())
	certPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPem := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer})
	return string(certPem), string(keyPem)
}

var _ = Describe("Certificate", func() {
	var server *testServer
	var certPem, keyPem string
	BeforeEach(func() {
		server = newTestServer(instantiaters.NewNull())
		certPem, keyPem = generateCertPair()
	})
	newCert := func(name string) *cert.Certificate {
		return &cert.Certificate{
			Name:       name,
			CertChain:  "\n" + certPem + "\n",
			PrivateKey: keyPem,
			Ca:         certPem,
		}
	}

	Context("CreateCertificate", func() {
		It("should create certificate with trimmed content", func() {
			resp, err := server.CreateCertificate(userCtx("bob"), &svccert.CreateCertificateRequest{Certificate: newCert("cert-1")})
			Expect(err).ToNot(HaveOccurred())

			stored, err := dbStoreMan.Certificates().Read(context.Background(), resp.GetCertificate().GetUUID())
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetCertChain()).To(HavePrefix("-----BEGIN CERTIFICATE-----"))
			Expect(stored.GetPrivateKey()).ToNot(BeEmpty())
		})
		It("answers AlreadyExists for a name already taken", func() {
			_, err := server.CreateCertificate(userCtx("bob"), &svccert.CreateCertificateRequest{Certificate: newCert("cert-1")})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.CreateCertificate(userCtx("bob"), &svccert.CreateCertificateRequest{Certificate: newCert("cert-1")})

			expectCode(err, connect.CodeAlreadyExists)
		})
		It("never returns the private key it was given", func() {
			resp, err := server.CreateCertificate(userCtx("bob"), &svccert.CreateCertificateRequest{Certificate: newCert("cert-1")})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetCertificate().GetPrivateKey()).To(BeEmpty())
			Expect(resp.GetCertificate().GetCertChain()).ToNot(BeEmpty())
		})
		DescribeTable("should refuse invalid pem content",
			func(modify func(c *cert.Certificate), errMsg string) {
				invalid := newCert("cert-1")
				modify(invalid)

				_, err := server.CreateCertificate(userCtx("bob"), &svccert.CreateCertificateRequest{Certificate: invalid})

				expectCode(err, connect.CodeInvalidArgument)
				Expect(err).To(MatchError(ContainSubstring(errMsg)))
			},
			Entry("invalid ca", func(c *cert.Certificate) { c.Ca = "not pem" }, "invalid ca"),
			Entry("invalid chain", func(c *cert.Certificate) { c.CertChain = "not pem" }, "invalid certChain"),
			Entry("invalid key", func(c *cert.Certificate) { c.PrivateKey = "not pem" }, "invalid privateKey"),
			Entry("key not matching chain", func(c *cert.Certificate) {
				_, otherKey := generateCertPair()
				c.PrivateKey = otherKey
			}, "invalid certChain or privateKey"),
		)
	})

	Context("existing certificate", func() {
		var certUuid string
		BeforeEach(func() {
			resp, err := server.CreateCertificate(userCtx("bob"), &svccert.CreateCertificateRequest{Certificate: newCert("cert-1")})
			Expect(err).ToNot(HaveOccurred())
			certUuid = resp.GetCertificate().GetUUID()
		})

		It("should hide private key on read", func() {
			resp, err := server.ReadCertificate(userCtx("bob"), &svccore.UuidRequest{UUID: certUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetCertificate().GetCertChain()).ToNot(BeEmpty())
			Expect(resp.GetCertificate().GetPrivateKey()).To(BeEmpty())
		})
		It("should hide private key on list", func() {
			resp, err := server.ListCertificate(userCtx("bob"), &svccert.ListCertificateRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetCertificates()).To(HaveLen(1))
			Expect(resp.GetCertificates()[0].GetPrivateKey()).To(BeEmpty())
		})
		It("answers NotFound for a deleted certificate", func() {
			_, err := server.DeleteCertificate(userCtx("bob"), &svccore.UuidRequest{UUID: certUuid})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.ReadCertificate(userCtx(adminUser), &svccore.UuidRequest{UUID: certUuid})
			expectCode(err, connect.CodeNotFound)
		})
		It("should be readable by owner only", func() {
			_, err := server.ReadCertificate(userCtx("alice"), &svccore.UuidRequest{UUID: certUuid})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("should update certificate", func() {
			updated := newCert("cert-1")
			updated.DomainNames = []string{"test.local"}

			_, err := server.UpdateCertificate(userCtx("bob"), &svccert.UpdateCertificateRequest{UUID: certUuid, Certificate: updated})
			Expect(err).ToNot(HaveOccurred())

			stored, err := dbStoreMan.Certificates().Read(context.Background(), certUuid)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetDomainNames()).To(Equal([]string{"test.local"}))
		})
		It("stores a typed private key without returning it", func() {
			newCertPem, newKeyPem := generateCertPair()
			updated := newCert("cert-1")
			updated.CertChain = newCertPem
			updated.PrivateKey = newKeyPem

			resp, err := server.UpdateCertificate(userCtx("bob"), &svccert.UpdateCertificateRequest{UUID: certUuid, Certificate: updated})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetCertificate().GetPrivateKey()).To(BeEmpty())

			stored, err := dbStoreMan.Certificates().Read(context.Background(), certUuid)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetPrivateKey()).To(Equal(strings.TrimSpace(newKeyPem)))
		})
		It("keeps the stored private key when the update has none", func() {
			before, err := dbStoreMan.Certificates().Read(context.Background(), certUuid)
			Expect(err).ToNot(HaveOccurred())
			originalKey := before.GetPrivateKey()
			Expect(originalKey).ToNot(BeEmpty())
			updated := newCert("cert-renamed")
			updated.PrivateKey = ""

			resp, err := server.UpdateCertificate(userCtx("bob"), &svccert.UpdateCertificateRequest{UUID: certUuid, Certificate: updated})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetCertificate().GetPrivateKey()).To(BeEmpty())

			stored, err := dbStoreMan.Certificates().Read(context.Background(), certUuid)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetName()).To(Equal("cert-renamed"))
			Expect(stored.GetPrivateKey()).To(Equal(originalKey))
		})
		It("should refuse update with invalid content", func() {
			invalid := newCert("cert-1")
			invalid.Ca = "not pem"

			_, err := server.UpdateCertificate(userCtx("bob"), &svccert.UpdateCertificateRequest{UUID: certUuid, Certificate: invalid})

			expectCode(err, connect.CodeInvalidArgument)
		})
		It("should delete certificate", func() {
			_, err := server.DeleteCertificate(userCtx("bob"), &svccore.UuidRequest{UUID: certUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(dbStoreMan.Certificates().Exists(context.Background(), certUuid)).To(BeFalse())
		})
		It("should refuse to delete certificate used by a virtual host of a virtual server", func() {
			backendResp, err := server.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-1")})
			Expect(err).ToNot(HaveOccurred())
			virtHost := newVirtualHost(backendResp.GetBackend().GetUUID())
			virtHost.TlsOptions = []*corev1.TLS{{ServerNames: []string{"test.local"}, CertificatesUuid: []string{certUuid}}}
			_, err = server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newHttpVs("vs-1", 443, virtHost)})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.DeleteCertificate(userCtx("bob"), &svccore.UuidRequest{UUID: certUuid})

			expectCode(err, connect.CodeFailedPrecondition)
		})
	})
})
