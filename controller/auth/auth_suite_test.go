package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"maps"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAuth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Auth Suite")
}

// fakeProvider is an OIDC provider serving its discovery document and its keys, it answers 503 while down
type fakeProvider struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	down   atomic.Bool
	// hangKeys makes the keys endpoint hold the request until the client gives up
	hangKeys atomic.Bool
}

func newFakeProvider() *fakeProvider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).ToNot(HaveOccurred())
	provider := &fakeProvider{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		if provider.down.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                provider.server.URL,
			"jwks_uri":                              provider.server.URL + "/keys",
			"authorization_endpoint":                provider.server.URL + "/auth",
			"token_endpoint":                        provider.server.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, req *http.Request) {
		if provider.hangKeys.Load() {
			<-req.Context().Done()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "key-1", "alg": "RS256", "use": "sig",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	provider.server = httptest.NewServer(mux)
	DeferCleanup(provider.server.Close)
	return provider
}

func (p *fakeProvider) Issuer() string {
	return p.server.URL
}

// sign gives a token of the provider, given claims override a valid issuer and valid dates
func (p *fakeProvider) sign(claims jwt.MapClaims) string {
	now := time.Now()
	full := jwt.MapClaims{"iss": p.server.URL, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()}
	maps.Copy(full, claims)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, full)
	tok.Header["kid"] = "key-1"
	signed, err := tok.SignedString(p.key)
	Expect(err).ToNot(HaveOccurred())
	return signed
}
