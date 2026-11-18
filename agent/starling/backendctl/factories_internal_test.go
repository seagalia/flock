package backendctl

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os/exec"
	"time"

	"github.com/ArthurHlt/gohc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
)

func generateCaPem() string {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).ToNot(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).ToNot(HaveOccurred())
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

var _ = Describe("Health check factories", func() {
	baseHc := func() *core.HealthCheck {
		return &core.HealthCheck{
			Timeout:  durationpb.New(1),
			Interval: durationpb.New(1),
		}
	}

	Context("makeHealthCheck", func() {
		DescribeTable("should create the right health checker for each kind",
			func(checker any, expectedType any) {
				hc := baseHc()
				switch c := checker.(type) {
				case *core.HttpHealthCheck:
					hc.HealthChecker = &core.HealthCheck_HttpHealthCheck{HttpHealthCheck: c}
				case *core.TcpHealthCheck:
					hc.HealthChecker = &core.HealthCheck_TcpHealthCheck{TcpHealthCheck: c}
				case *core.GrpcHealthCheck:
					hc.HealthChecker = &core.HealthCheck_GrpcHealthCheck{GrpcHealthCheck: c}
				case *core.UdpHealthCheck:
					hc.HealthChecker = &core.HealthCheck_UdpHealthCheck{UdpHealthCheck: c}
				case *core.NoHealthCheck:
					hc.HealthChecker = &core.HealthCheck_NoHealthCheck{NoHealthCheck: c}
				}
				hchecker, err := makeHealthCheck(hc, "", "")
				Expect(err).ToNot(HaveOccurred())
				Expect(hchecker).To(BeAssignableToTypeOf(expectedType))
			},
			Entry("http", &core.HttpHealthCheck{Path: "/health", Method: core.RequestMethod_POST}, &gohc.HttpHealthCheck{}),
			Entry("tcp", &core.TcpHealthCheck{}, &gohc.TcpHealthCheck{}),
			Entry("grpc", &core.GrpcHealthCheck{ServiceName: "svc"}, &gohc.GrpcHealthCheck{}),
			Entry("udp", &core.UdpHealthCheck{}, &gohc.UdpHealthCheck{}),
			Entry("none", &core.NoHealthCheck{}, &gohc.NoHealthCheck{}),
		)

		It("should give no health checker when health check kind is not set", func() {
			hchecker, err := makeHealthCheck(baseHc(), "", "")
			Expect(err).ToNot(HaveOccurred())
			Expect(hchecker).To(BeNil())
		})

		Context("plugin health check", func() {
			AfterEach(func() {
				SetPluginsHc(make([]*config.PluginHealthCheckConfig, 0))
			})

			It("should fail when plugin is not registered", func() {
				hc := baseHc()
				hc.HealthChecker = &core.HealthCheck_PluginHealthCheck{PluginHealthCheck: &core.PluginHealthCheck{Name: "unknown"}}
				_, err := makeHealthCheck(hc, "", "")
				Expect(err).To(MatchError(ContainSubstring("plugin unknown not found")))
			})

			It("should create a program health check when plugin is registered", func() {
				lsPath, err := exec.LookPath("ls")
				Expect(err).ToNot(HaveOccurred())
				SetPluginsHc([]*config.PluginHealthCheckConfig{{Name: "my-plugin", Description: "desc", Path: lsPath}})

				opts, err := structpb.NewStruct(map[string]any{"key": "value"})
				Expect(err).ToNot(HaveOccurred())
				hc := baseHc()
				hc.HealthChecker = &core.HealthCheck_PluginHealthCheck{PluginHealthCheck: &core.PluginHealthCheck{Name: "my-plugin", Options: opts}}
				hchecker, err := makeHealthCheck(hc, "my.server", generateCaPem())
				Expect(err).ToNot(HaveOccurred())
				Expect(hchecker).To(BeAssignableToTypeOf(&gohc.ProgramHealthCheck{}))
			})
		})
	})

	Context("makeTlsConfig", func() {
		It("should give no tls config when tls is disabled", func() {
			Expect(makeTlsConfig(false, "my.server", generateCaPem())).To(BeNil())
		})

		It("should give a tls config with server name when tls is enabled", func() {
			tlsConf := makeTlsConfig(true, "my.server", "")
			Expect(tlsConf).ToNot(BeNil())
			Expect(tlsConf.ServerName).To(Equal("my.server"))
			Expect(tlsConf.RootCAs).To(BeNil(), "system pool must be used when no ca given")
		})

		It("should load given root ca when tls is enabled", func() {
			caPem := generateCaPem()
			expectedPool := x509.NewCertPool()
			Expect(expectedPool.AppendCertsFromPEM([]byte(caPem))).To(BeTrue())

			tlsConf := makeTlsConfig(true, "", caPem)
			Expect(tlsConf).ToNot(BeNil())
			Expect(tlsConf.RootCAs.Equal(expectedPool)).To(BeTrue())
		})
	})

	Context("payload helpers", func() {
		It("should convert text and binary payloads", func() {
			Expect(makeGohcPayload(nil)).To(BeNil())
			Expect(makeGohcPayload(&core.HealthCheckPayload{Payload: &core.HealthCheckPayload_Text{Text: "PING"}}).Text).To(Equal("PING"))
			Expect(makeGohcPayload(&core.HealthCheckPayload{Payload: &core.HealthCheckPayload_Binary{Binary: []byte{0x1}}}).Binary).To(Equal([]byte{0x1}))
		})

		It("should convert a list of payloads", func() {
			Expect(makeGohcPayloads(nil)).To(BeNil())
			payloads := makeGohcPayloads([]*core.HealthCheckPayload{
				{Payload: &core.HealthCheckPayload_Text{Text: "a"}},
				{Payload: &core.HealthCheckPayload_Text{Text: "b"}},
			})
			Expect(payloads).To(HaveLen(2))
			Expect(payloads[1].Text).To(Equal("b"))
		})

		It("should convert headers honoring append flag", func() {
			headers := makeGohcHeader([]*core.HeaderValueOption{
				{Header: &core.HeaderValue{Key: "X-Set", ValueSource: &core.HeaderValue_Value{Value: "1"}}},
				{Header: &core.HeaderValue{Key: "X-Set", ValueSource: &core.HeaderValue_Value{Value: "2"}}},
				{Header: &core.HeaderValue{Key: "X-Append", ValueSource: &core.HeaderValue_Value{Value: "1"}}, Append: true},
				{Header: &core.HeaderValue{Key: "X-Append", ValueSource: &core.HeaderValue_Value{Value: "2"}}, Append: true},
			})
			Expect(headers).To(Equal(http.Header{
				"X-Set":    []string{"2"},
				"X-Append": []string{"1", "2"},
			}))
		})

		It("should convert expected statuses", func() {
			Expect(makeGohcExpectedStatus(nil)).To(BeNil())
			Expect(makeGohcExpectedStatus(&flocktype.Int64Range{Start: 200, End: 300})).To(Equal(&gohc.IntRange{Start: 200, End: 300}))
		})
	})
})
