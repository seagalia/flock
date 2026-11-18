package envoyctl

import (
	"time"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	proxy_protocolv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/proxy_protocol/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	starling_commopts "github.com/seagalia/flock/sdk/flock/extensions/common/starling_options/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var _ = Describe("Backend conversion", func() {
	var aBackend *backend.Backend

	BeforeEach(func() {
		aBackend = &backend.Backend{
			UUID:           "backend-uuid",
			Name:           "my-backend",
			ConnectTimeout: durationpb.New(3 * time.Second),
			LbPolicy:       backend.Backend_ROUND_ROBIN,
			Endpoint: &backend.Backend_IpAddrGroup{
				IpAddrGroup: &core.IPAddrGroup{IpAddresses: []*core.IPAddr{{Ip: "10.0.0.1", Port: 80}}},
			},
		}
	})

	Context("makeEnvoyCluster", func() {
		It("should create a valid eds backend fed by ads", func() {
			envoyCluster, err := makeEnvoyCluster(aBackend, nil)
			Expect(err).ToNot(HaveOccurred())
			expectValidEnvoyConfig(envoyCluster)

			Expect(envoyCluster.GetName()).To(Equal("backend-uuid"))
			Expect(envoyCluster.GetAltStatName()).To(Equal("my-backend"))
			Expect(envoyCluster.GetType()).To(Equal(clusterv3.Cluster_EDS))
			Expect(envoyCluster.GetEdsClusterConfig().GetEdsConfig().GetAds()).ToNot(BeNil())
			Expect(envoyCluster.GetConnectTimeout().AsDuration()).To(Equal(3 * time.Second))
			Expect(envoyCluster.GetPerConnectionBufferLimitBytes().GetValue()).To(Equal(uint32(32768)))
			Expect(envoyCluster.GetTransportSocket()).To(BeNil())
			Expect(envoyCluster.GetTypedExtensionProtocolOptions()).To(BeEmpty())
		})

		It("should default connect timeout to 5s", func() {
			aBackend.ConnectTimeout = nil
			envoyCluster, err := makeEnvoyCluster(aBackend, nil)
			Expect(err).ToNot(HaveOccurred())
			Expect(envoyCluster.GetConnectTimeout().AsDuration()).To(Equal(5 * time.Second))
		})

		It("should fail when http protocol options are not of agent type", func() {
			wrongType, err := anyOf(&wrapperspb.StringValue{Value: "wrong"})
			Expect(err).ToNot(HaveOccurred())
			aBackend.HttpProtocolOptions = wrongType
			_, err = makeEnvoyCluster(aBackend, nil)
			Expect(err).To(MatchError(ContainSubstring("failed to get agent http protocol options")))
		})

		DescribeTable("should convert lb policy",
			func(policy backend.Backend_LbPolicy, expected clusterv3.Cluster_LbPolicy) {
				Expect(lbPolicyToLbEnvoy(policy)).To(Equal(expected))
				aBackend.LbPolicy = policy
				envoyCluster, err := makeEnvoyCluster(aBackend, nil)
				Expect(err).ToNot(HaveOccurred())
				expectValidEnvoyConfig(envoyCluster)
			},
			Entry("round robin", backend.Backend_ROUND_ROBIN, clusterv3.Cluster_ROUND_ROBIN),
			Entry("weighted round robin", backend.Backend_WEIGHTED_ROUND_ROBIN, clusterv3.Cluster_ROUND_ROBIN),
			Entry("least request", backend.Backend_LEAST_REQUEST, clusterv3.Cluster_LEAST_REQUEST),
			Entry("weighted least request", backend.Backend_WEIGHTED_LEAST_REQUEST, clusterv3.Cluster_LEAST_REQUEST),
			Entry("ring hash", backend.Backend_RING_HASH, clusterv3.Cluster_RING_HASH),
			Entry("maglev as default", backend.Backend_MAGLEV, clusterv3.Cluster_MAGLEV),
		)
	})

	Context("backendTlsToTransportSocket", func() {
		It("should give no transport socket when tls is disabled", func() {
			Expect(backendTlsToTransportSocket(aBackend)).To(BeNil())
		})

		It("should use given ca and sni when tls is enabled", func() {
			aBackend.EnableTls = true
			aBackend.Ca = "my-ca-pem"
			aBackend.Sni = "backend.local"

			transport := backendTlsToTransportSocket(aBackend)
			Expect(transport.GetName()).To(Equal("envoy.transport_sockets.tls"))
			tlsCtx := unmarshalAny(transport.GetTypedConfig(), &tlsv3.UpstreamTlsContext{})
			Expect(tlsCtx.GetSni()).To(Equal("backend.local"))
			Expect(tlsCtx.GetCommonTlsContext().GetValidationContext().GetTrustedCa().GetInlineString()).To(Equal("my-ca-pem"))

			envoyCluster, err := makeEnvoyCluster(aBackend, transport)
			Expect(err).ToNot(HaveOccurred())
			expectValidEnvoyConfig(envoyCluster)
		})

		It("should fallback to system ca when no ca given", func() {
			aBackend.EnableTls = true
			tlsCtx := unmarshalAny(backendTlsToTransportSocket(aBackend).GetTypedConfig(), &tlsv3.UpstreamTlsContext{})
			systemPath := findSystemCertPath()
			if systemPath == "" {
				Expect(tlsCtx.GetCommonTlsContext()).To(BeNil())
				return
			}
			Expect(tlsCtx.GetCommonTlsContext().GetValidationContext().GetTrustedCa().GetFilename()).To(Equal(systemPath))
		})
	})

	Context("backendProxyProtocolTransportSocket", func() {
		DescribeTable("should wrap transport with proxy protocol",
			func(version backend.Backend_ProxyProtocolVersion, expected corev3.ProxyProtocolConfig_Version) {
				transport := backendProxyProtocolTransportSocket(version, nil)
				Expect(transport.GetName()).To(Equal("envoy.transport_sockets.proxy_protocol"))
				proxyConf := unmarshalAny(transport.GetTypedConfig(), &proxy_protocolv3.ProxyProtocolUpstreamTransport{})
				Expect(proxyConf.GetConfig().GetVersion()).To(Equal(expected))
				Expect(proxyConf.GetTransportSocket().GetName()).To(Equal("envoy.transport_sockets.raw_buffer"))
			},
			Entry("v1", backend.Backend_V1, corev3.ProxyProtocolConfig_V1),
			Entry("v2", backend.Backend_V2, corev3.ProxyProtocolConfig_V2),
		)

		It("should wrap an existing tls transport", func() {
			aBackend.EnableTls = true
			tlsTransport := backendTlsToTransportSocket(aBackend)
			transport := backendProxyProtocolTransportSocket(backend.Backend_V2, tlsTransport)
			proxyConf := unmarshalAny(transport.GetTypedConfig(), &proxy_protocolv3.ProxyProtocolUpstreamTransport{})
			Expect(proxyConf.GetTransportSocket().GetName()).To(Equal("envoy.transport_sockets.tls"))

			envoyCluster, err := makeEnvoyCluster(aBackend, transport)
			Expect(err).ToNot(HaveOccurred())
			expectValidEnvoyConfig(envoyCluster)
		})
	})

	Context("httpProtOptionsToEnvoyProtOptions", func() {
		It("should give nothing on nil options", func() {
			Expect(httpProtOptionsToEnvoyProtOptions(nil)).To(BeNil())
		})

		It("should convert common and upstream options", func() {
			opts := httpProtOptionsToEnvoyProtOptions(&starling_commopts.HttpProtocolOptions{
				CommonHttpProtocolOptions: &starling_commopts.CommonHttpProtocolOptions{
					IdleTimeout:     durationpb.New(time.Minute),
					MaxHeadersCount: wrapperspb.UInt32(100),
				},
				UpstreamHttpProtocolOptions: &starling_commopts.UpstreamHttpProtocolOptions{
					AutoSni:           true,
					AutoSanValidation: true,
				},
				UpstreamProtocolOptions: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_{
					ExplicitHttpConfig: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig{
						ProtocolConfig: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions{
							HttpProtocolOptions: &starling_commopts.Http1ProtocolOptions{EnableTrailers: true},
						},
					},
				},
			})
			expectValidEnvoyConfig(opts)
			Expect(opts.GetCommonHttpProtocolOptions().GetIdleTimeout().AsDuration()).To(Equal(time.Minute))
			Expect(opts.GetCommonHttpProtocolOptions().GetMaxHeadersCount().GetValue()).To(Equal(uint32(100)))
			Expect(opts.GetUpstreamHttpProtocolOptions().GetAutoSni()).To(BeTrue())
			Expect(opts.GetExplicitHttpConfig().GetHttpProtocolOptions().GetEnableTrailers()).To(BeTrue())
		})

		It("should apply edge proxy defaults on explicit http2", func() {
			opts := httpProtOptionsToEnvoyProtOptions(&starling_commopts.HttpProtocolOptions{
				UpstreamProtocolOptions: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_{
					ExplicitHttpConfig: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig{
						ProtocolConfig: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
							Http2ProtocolOptions: &starling_commopts.Http2ProtocolOptions{
								ConnectionKeepalive: &starling_commopts.KeepaliveSettings{
									Interval: durationpb.New(10 * time.Second),
									Timeout:  durationpb.New(time.Second),
								},
							},
						},
					},
				},
			})
			expectValidEnvoyConfig(opts)
			http2 := opts.GetExplicitHttpConfig().GetHttp2ProtocolOptions()
			Expect(http2.GetInitialStreamWindowSize().GetValue()).To(Equal(uint32(65536)))
			Expect(http2.GetInitialConnectionWindowSize().GetValue()).To(Equal(uint32(1048576)))
			Expect(http2.GetConnectionKeepalive().GetInterval().AsDuration()).To(Equal(10 * time.Second))
		})

		It("should keep user window sizes on explicit http2", func() {
			opts := httpProtOptionsToEnvoyProtOptions(&starling_commopts.HttpProtocolOptions{
				UpstreamProtocolOptions: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_{
					ExplicitHttpConfig: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig{
						ProtocolConfig: &starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
							Http2ProtocolOptions: &starling_commopts.Http2ProtocolOptions{
								InitialStreamWindowSize:     wrapperspb.UInt32(70000),
								InitialConnectionWindowSize: wrapperspb.UInt32(80000),
							},
						},
					},
				},
			})
			expectValidEnvoyConfig(opts)
			http2 := opts.GetExplicitHttpConfig().GetHttp2ProtocolOptions()
			Expect(http2.GetInitialStreamWindowSize().GetValue()).To(Equal(uint32(70000)))
			Expect(http2.GetInitialConnectionWindowSize().GetValue()).To(Equal(uint32(80000)))
		})

		It("should convert auto config", func() {
			opts := httpProtOptionsToEnvoyProtOptions(&starling_commopts.HttpProtocolOptions{
				UpstreamProtocolOptions: &starling_commopts.HttpProtocolOptions_AutoConfig{
					AutoConfig: &starling_commopts.HttpProtocolOptions_AutoHttpConfig{
						HttpProtocolOptions:  &starling_commopts.Http1ProtocolOptions{SendFullyQualifiedUrl: true},
						Http2ProtocolOptions: &starling_commopts.Http2ProtocolOptions{},
					},
				},
			})
			expectValidEnvoyConfig(opts)
			Expect(opts.GetUpstreamProtocolOptions()).To(BeAssignableToTypeOf(&httpv3.HttpProtocolOptions_AutoConfig{}))
			Expect(opts.GetAutoConfig().GetHttpProtocolOptions().GetSendFullyQualifiedUrl()).To(BeTrue())
			Expect(opts.GetAutoConfig().GetHttp2ProtocolOptions().GetInitialStreamWindowSize().GetValue()).To(Equal(uint32(65536)))
		})

		It("should convert use downstream protocol config", func() {
			opts := httpProtOptionsToEnvoyProtOptions(&starling_commopts.HttpProtocolOptions{
				UpstreamProtocolOptions: &starling_commopts.HttpProtocolOptions_UseDownstreamProtocolConfig{
					UseDownstreamProtocolConfig: &starling_commopts.HttpProtocolOptions_UseDownstreamHttpConfig{
						Http2ProtocolOptions: &starling_commopts.Http2ProtocolOptions{MaxConcurrentStreams: wrapperspb.UInt32(10)},
					},
				},
			})
			expectValidEnvoyConfig(opts)
			Expect(opts.GetUseDownstreamProtocolConfig().GetHttp2ProtocolOptions().GetMaxConcurrentStreams().GetValue()).To(Equal(uint32(10)))
		})
	})
})
