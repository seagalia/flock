package params_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"time"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-viper/mapstructure/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/provisioners/acme/params"
)

func newKey() *ecdsa.PrivateKey {
	GinkgoHelper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	return key
}

func decode(metadata map[string]string) (*params.CertRequest, error) {
	req := &params.CertRequest{}
	err := mapstructure.Decode(metadata, req)
	return req, err
}

var _ = Describe("CertRequest", func() {
	It("reads the acme keys of the metadata and keeps the other keys for the dns provider", func() {
		req, err := decode(map[string]string{
			"flock.acme.email":               "ops@example.com",
			"flock.acme.ca_dir_url":          "https://ca.example.com/dir",
			"flock.acme.ovh.application_key": "ak",
			"flock.provisioner.acme":         "true",
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(req.GetEmail()).To(Equal("ops@example.com"))
		Expect(req.CADirURL).To(Equal("https://ca.example.com/dir"))
		Expect(req.DnsProvider).To(HaveKeyWithValue("flock.acme.ovh.application_key", "ak"))
		Expect(req.GetRegistration()).To(BeNil())
		Expect(req.GetPrivateKey()).To(BeNil())
	})

	It("defaults the key type to EC256", func() {
		req, err := decode(map[string]string{})

		Expect(err).NotTo(HaveOccurred())
		Expect(req.KeyType).To(Equal("EC256"))
	})

	It("refuses an unknown key type", func() {
		_, err := decode(map[string]string{"flock.acme.key_type": "dsa"})

		Expect(err).To(MatchError(ContainSubstring("invalid key type")))
	})

	It("reads back the account and the key it wrote", func() {
		key := newKey()
		written := &params.CertRequest{
			Email:   "ops@example.com",
			KeyType: "EC256",
			Registration: &params.RegistrationRequest{ExtendedAccount: &acme.ExtendedAccount{
				Status: "valid", Contact: []string{"mailto:ops@example.com"},
				Location: "https://ca.example.com/acct/1",
			}},
			KeyPem: &params.KeyPemRequest{Signer: key},
		}

		metadata, err := written.ToMap()
		Expect(err).NotTo(HaveOccurred())
		read, err := decode(metadata)

		Expect(err).NotTo(HaveOccurred())
		Expect(read.GetRegistration().Location).To(Equal("https://ca.example.com/acct/1"))
		Expect(read.GetRegistration().Status).To(Equal("valid"))
		Expect(read.GetPrivateKey().Public()).To(Equal(key.Public()))
	})

	It("refuses a key that isn't a PKCS #8 PEM block", func() {
		_, err := decode(map[string]string{"flock.acme.key_pem": "not a pem"})

		Expect(err).To(MatchError(ContainSubstring("invalid PEM block")))
	})

	Describe("the secret maps", func() {
		var req *params.CertRequest

		BeforeEach(func() {
			req = &params.CertRequest{
				Email:        "ops@example.com",
				CADirURL:     "https://ca.example.com/dir",
				KeyType:      "EC256",
				Registration: &params.RegistrationRequest{ExtendedAccount: &acme.ExtendedAccount{Location: "loc"}},
				KeyPem:       &params.KeyPemRequest{Signer: newKey()},
			}
		})

		It("puts only the account and its key in the secret", func() {
			secretMap, err := req.ToSecretMap()

			Expect(err).NotTo(HaveOccurred())
			Expect(secretMap).To(HaveLen(2))
			Expect(secretMap).To(HaveKey("flock.acme.registration"))
			Expect(secretMap).To(HaveKey("flock.acme.key_pem"))
		})

		It("points the metadata at the secret and keeps the plain settings", func() {
			refs, err := req.ToMapSecretRef("secret-1")

			Expect(err).NotTo(HaveOccurred())
			Expect(refs).To(Equal(map[string]string{
				"flock.acme.registration": "secret://secret-1/flock.acme.registration",
				"flock.acme.key_pem":      "secret://secret-1/flock.acme.key_pem",
				"flock.acme.email":        "ops@example.com",
				"flock.acme.ca_dir_url":   "https://ca.example.com/dir",
				"flock.acme.key_type":     "EC256",
			}))
		})
	})
})

var _ = Describe("OvhParams", func() {
	It("keeps the defaults of lego for the values left empty", func() {
		cnf, err := (&params.OvhParams{ApplicationKey: "ak", PollingInterval: "5s"}).ToConfig()

		Expect(err).NotTo(HaveOccurred())
		Expect(cnf.ApplicationKey).To(Equal("ak"))
		Expect(cnf.PollingInterval).To(Equal(5 * time.Second))
		Expect(cnf.PropagationTimeout).To(Equal(60 * time.Second))
	})

	It("names the param that doesn't parse", func() {
		_, err := (&params.OvhParams{PropagationTimeout: "soon", TTL: "many"}).ToConfig()

		Expect(err).To(MatchError(ContainSubstring("propagation_timeout")))
		Expect(err).To(MatchError(ContainSubstring("ttl")))
	})
})
