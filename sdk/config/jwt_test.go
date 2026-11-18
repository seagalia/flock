package config_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v2"

	. "github.com/seagalia/flock/sdk/config"
)

func rsaPrivatePem() string {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

// indent nests a multi-line value under a yaml literal block
func indent(text string) string {
	return "  " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ") + "\n"
}

var _ = Describe("Jwt config", func() {
	Context("JwtAlg", func() {
		It("should load a known signing algorithm", func() {
			var alg JwtAlg
			Expect(yaml.Unmarshal([]byte("HS256"), &alg)).To(Succeed())
			Expect(alg.AlgRaw).To(Equal("HS256"))
			Expect(alg.Alg).To(Equal(jwt.SigningMethodHS256))
		})

		It("should reject an unknown algorithm", func() {
			var alg JwtAlg
			Expect(yaml.Unmarshal([]byte("NOPE"), &alg)).To(MatchError(ContainSubstring("NOPE")))
		})

		It("should write back the algorithm name it reads", func() {
			written, err := yaml.Marshal(JwtMaker{Alg: JwtAlg{AlgRaw: "HS512"}, PrivateKey: "key"})
			Expect(err).ToNot(HaveOccurred())
			Expect(string(written)).To(ContainSubstring("alg: HS512\n"))

			var maker JwtMaker
			Expect(yaml.Unmarshal(written, &maker)).To(Succeed())
			Expect(maker.Alg.Alg).To(Equal(jwt.SigningMethodHS512))
		})
	})

	Context("JwtMaker", func() {
		It("should set default expiration, audience and issuer", func() {
			var maker JwtMaker
			Expect(yaml.Unmarshal([]byte("alg: HS256\nprivate_key: secret\n"), &maker)).To(Succeed())
			Expect(time.Duration(maker.ExpireIn)).To(Equal(3 * time.Hour))
			Expect(maker.Audience).To(Equal("flock"))
			Expect(maker.Issuer).To(Equal("flock"))
		})

		It("should keep given expiration and audience", func() {
			var maker JwtMaker
			raw := "alg: HS256\nprivate_key: secret\nexpire_in: 10m\naudience: other\n"
			Expect(yaml.Unmarshal([]byte(raw), &maker)).To(Succeed())
			Expect(time.Duration(maker.ExpireIn)).To(Equal(10 * time.Minute))
			Expect(maker.Audience).To(Equal("other"))
		})

		DescribeTable("should reject incomplete makers",
			func(raw string, expectedErr string) {
				var maker JwtMaker
				Expect(yaml.Unmarshal([]byte(raw), &maker)).To(MatchError(ContainSubstring(expectedErr)))
			},
			Entry("without key", "alg: HS256\n", "private key is required"),
			Entry("without algorithm", "private_key: secret\n", "algorithm is required"),
			Entry("with a key that is not a pem", "alg: RS256\nprivate_key: secret\n", "jwt_maker private key for RS256"),
			Entry("with a pem of another algorithm", "alg: ES256\nprivate_key: |\n"+indent(rsaPrivatePem()),
				"jwt_maker private key for ES256"),
			Entry("with an EdDSA key that is not a pem", "alg: EdDSA\nprivate_key: secret\n",
				"jwt_maker private key for EdDSA"),
		)
	})
})
