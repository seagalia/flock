package envoyctl

import (
	"fmt"
	"os"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	proxy_protocolv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/proxy_protocol/v3"
	raw_bufferv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/raw_buffer/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	starling_commopts "github.com/seagalia/flock/sdk/flock/extensions/common/starling_options/v1"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var systemCertPath = []string{
	"/etc/ssl/certs/ca-certificates.crt",                // Debian/Ubuntu/Gentoo etc.
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", // CentOS/RHEL 7
	"/etc/pki/tls/certs/ca-bundle.crt",                  // Fedora/RHEL 6
	"/etc/ssl/ca-bundle.pem",                            // OpenSUSE
	"/usr/local/etc/ssl/cert.pem",                       // FreeBSD
	"/etc/ssl/cert.pem",                                 // OpenBSD
}

func findSystemCertPath() string {
	for _, path := range systemCertPath {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func makeEnvoyCluster(aBackend *backend.Backend, transportSocket *corev3.TransportSocket) (*clusterv3.Cluster, error) {
	typedExt := make(map[string]*anypb.Any)
	httpOptsRaw, err := aBackend.GetStarlingHttpProtocolOptions()
	if err != nil {
		return nil, fmt.Errorf("failed to get agent http protocol options: %w", err)
	}
	httpOpts := httpProtOptionsToEnvoyProtOptions(httpOptsRaw)
	if httpOpts != nil {
		httpOptsAny, _ := anypb.New(httpOpts) // nolint
		typedExt["envoy.extensions.upstreams.http.v3.HttpProtocolOptions"] = httpOptsAny
	}
	connectTimeout := aBackend.GetConnectTimeout()
	if connectTimeout == nil {
		connectTimeout = &durationpb.Duration{
			Seconds: 5,
		}
	}
	return &clusterv3.Cluster{
		Name:                 aBackend.GetUUID(),
		AltStatName:          aBackend.GetName(),
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_EDS},
		EdsClusterConfig: &clusterv3.Cluster_EdsClusterConfig{
			EdsConfig: &corev3.ConfigSource{
				ConfigSourceSpecifier: &corev3.ConfigSource_Ads{
					Ads: &corev3.AggregatedConfigSource{},
				},
				ResourceApiVersion: corev3.ApiVersion_V3,
			},
		},
		ConnectTimeout: connectTimeout,
		PerConnectionBufferLimitBytes: &wrapperspb.UInt32Value{
			Value: 32768, // 32 KiB as suggested for envoy as edge proxy
		},
		LbPolicy:                      lbPolicyToLbEnvoy(aBackend.GetLbPolicy()),
		TransportSocket:               transportSocket,
		TypedExtensionProtocolOptions: typedExt,
	}, nil
}

func lbPolicyToLbEnvoy(policy backend.Backend_LbPolicy) clusterv3.Cluster_LbPolicy {
	switch policy {
	case backend.Backend_ROUND_ROBIN:
		return clusterv3.Cluster_ROUND_ROBIN
	case backend.Backend_WEIGHTED_ROUND_ROBIN:
		return clusterv3.Cluster_ROUND_ROBIN
	case backend.Backend_LEAST_REQUEST:
		return clusterv3.Cluster_LEAST_REQUEST
	case backend.Backend_WEIGHTED_LEAST_REQUEST:
		return clusterv3.Cluster_LEAST_REQUEST
	case backend.Backend_RING_HASH:
		return clusterv3.Cluster_RING_HASH
	}
	return clusterv3.Cluster_MAGLEV
}

func backendTlsToTransportSocket(aBackend *backend.Backend) *corev3.TransportSocket {
	if !aBackend.EnableTls {
		return nil
	}
	var commonTlsContext *tlsv3.CommonTlsContext
	var trustedCa *corev3.DataSource
	systemCertPath := findSystemCertPath()
	if systemCertPath != "" {
		trustedCa = &corev3.DataSource{
			Specifier: &corev3.DataSource_Filename{
				Filename: systemCertPath,
			},
		}
	}
	if aBackend.GetCa() != "" {
		trustedCa = &corev3.DataSource{
			Specifier: &corev3.DataSource_InlineString{
				InlineString: aBackend.GetCa(),
			},
		}
	}
	if trustedCa != nil {
		commonTlsContext = &tlsv3.CommonTlsContext{
			ValidationContextType: &tlsv3.CommonTlsContext_ValidationContext{
				ValidationContext: &tlsv3.CertificateValidationContext{
					TrustedCa: trustedCa,
				},
			},
		}
	}
	conf, _ := anypb.New(&tlsv3.UpstreamTlsContext{ // nolint
		CommonTlsContext: commonTlsContext,
		Sni:              aBackend.GetSni(),
	})
	return &corev3.TransportSocket{
		Name: "envoy.transport_sockets.tls",
		ConfigType: &corev3.TransportSocket_TypedConfig{
			TypedConfig: conf,
		},
	}
}

func backendProxyProtocolTransportSocket(version backend.Backend_ProxyProtocolVersion, next *corev3.TransportSocket) *corev3.TransportSocket {
	if next == nil {
		rawConf, _ := anypb.New(&raw_bufferv3.RawBuffer{}) // nolint
		next = &corev3.TransportSocket{
			Name: "envoy.transport_sockets.raw_buffer",
			ConfigType: &corev3.TransportSocket_TypedConfig{
				TypedConfig: rawConf,
			},
		}
	}
	var versionEnvoy corev3.ProxyProtocolConfig_Version
	switch version {
	case backend.Backend_V1:
		versionEnvoy = corev3.ProxyProtocolConfig_V1
	case backend.Backend_V2:
		versionEnvoy = corev3.ProxyProtocolConfig_V2
	}
	proxyConf, _ := anypb.New(&proxy_protocolv3.ProxyProtocolUpstreamTransport{ // nolint
		TransportSocket: next,
		Config: &corev3.ProxyProtocolConfig{
			Version: versionEnvoy,
		},
	})
	return &corev3.TransportSocket{
		Name: "envoy.transport_sockets.proxy_protocol",
		ConfigType: &corev3.TransportSocket_TypedConfig{
			TypedConfig: proxyConf,
		},
	}
}

func httpProtOptionsToEnvoyProtOptions(protOpt *starling_commopts.HttpProtocolOptions) *httpv3.HttpProtocolOptions {
	if protOpt == nil {
		return nil
	}
	var commonHttpProtocolOptions *corev3.HttpProtocolOptions
	if protOpt.CommonHttpProtocolOptions != nil {
		commonHttpProtocolOptions = &corev3.HttpProtocolOptions{
			IdleTimeout:              protOpt.CommonHttpProtocolOptions.IdleTimeout,
			MaxConnectionDuration:    protOpt.CommonHttpProtocolOptions.MaxConnectionDuration,
			MaxHeadersCount:          protOpt.CommonHttpProtocolOptions.MaxHeadersCount,
			MaxStreamDuration:        protOpt.CommonHttpProtocolOptions.MaxStreamDuration,
			MaxRequestsPerConnection: protOpt.CommonHttpProtocolOptions.MaxRequestsPerConnection,
		}
	}
	var upstreamHttpProtocolOptions *corev3.UpstreamHttpProtocolOptions
	if protOpt.UpstreamHttpProtocolOptions != nil {
		upstreamHttpProtocolOptions = &corev3.UpstreamHttpProtocolOptions{
			AutoSni:               protOpt.UpstreamHttpProtocolOptions.AutoSni,
			AutoSanValidation:     protOpt.UpstreamHttpProtocolOptions.AutoSanValidation,
			OverrideAutoSniHeader: protOpt.UpstreamHttpProtocolOptions.OverrideAutoSniHeader,
		}
	}
	finalOpt := &httpv3.HttpProtocolOptions{
		CommonHttpProtocolOptions:   commonHttpProtocolOptions,
		UpstreamHttpProtocolOptions: upstreamHttpProtocolOptions,
		UpstreamProtocolOptions:     nil,
	}
	if protOpt.GetExplicitHttpConfig() != nil {
		switch opt := protOpt.GetExplicitHttpConfig().GetProtocolConfig().(type) {
		case *starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions:
			finalOpt.UpstreamProtocolOptions = &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{
				ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
					ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions{
						HttpProtocolOptions: &corev3.Http1ProtocolOptions{
							EnableTrailers:        opt.HttpProtocolOptions.EnableTrailers,
							SendFullyQualifiedUrl: opt.HttpProtocolOptions.SendFullyQualifiedUrl,
						},
					},
				},
			}
		case *starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions:
			initStreamWindowsSize := &wrapperspb.UInt32Value{Value: 65536}    // as suggested for envoy as edge proxy
			initConnectWindowsSize := &wrapperspb.UInt32Value{Value: 1048576} // as suggested for envoy as edge proxy
			if opt.Http2ProtocolOptions.InitialStreamWindowSize != nil {
				initStreamWindowsSize = opt.Http2ProtocolOptions.InitialStreamWindowSize
			}
			if opt.Http2ProtocolOptions.InitialConnectionWindowSize != nil {
				initConnectWindowsSize = opt.Http2ProtocolOptions.InitialConnectionWindowSize
			}
			finalOpt.UpstreamProtocolOptions = &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{
				ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
					ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
						Http2ProtocolOptions: &corev3.Http2ProtocolOptions{
							HpackTableSize:                               opt.Http2ProtocolOptions.HpackTableSize,
							MaxConcurrentStreams:                         opt.Http2ProtocolOptions.MaxConcurrentStreams,
							InitialStreamWindowSize:                      initStreamWindowsSize,
							InitialConnectionWindowSize:                  initConnectWindowsSize,
							AllowConnect:                                 opt.Http2ProtocolOptions.AllowConnect,
							MaxOutboundFrames:                            opt.Http2ProtocolOptions.MaxOutboundFrames,
							MaxOutboundControlFrames:                     opt.Http2ProtocolOptions.MaxOutboundControlFrames,
							MaxConsecutiveInboundFramesWithEmptyPayload:  opt.Http2ProtocolOptions.MaxConsecutiveInboundFramesWithEmptyPayload,
							MaxInboundPriorityFramesPerStream:            opt.Http2ProtocolOptions.MaxInboundPriorityFramesPerStream,
							MaxInboundWindowUpdateFramesPerDataFrameSent: opt.Http2ProtocolOptions.MaxInboundWindowUpdateFramesPerDataFrameSent,
							ConnectionKeepalive:                          coreKeepaliveToEnvoyKeepalive(opt.Http2ProtocolOptions.ConnectionKeepalive),
						},
					},
				},
			}
		case *starling_commopts.HttpProtocolOptions_ExplicitHttpConfig_Http3ProtocolOptions:
			finalOpt.UpstreamProtocolOptions = &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{
				ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
					ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_Http3ProtocolOptions{
						Http3ProtocolOptions: &corev3.Http3ProtocolOptions{
							QuicProtocolOptions:                     quicProtOptToQuicEnvoy(opt.Http3ProtocolOptions.QuicProtocolOptions),
							OverrideStreamErrorOnInvalidHttpMessage: opt.Http3ProtocolOptions.OverrideStreamErrorOnInvalidHttpMessage,
							AllowExtendedConnect:                    opt.Http3ProtocolOptions.AllowExtendedConnect,
						},
					},
				},
			}
		}

	}

	if protOpt.GetAutoConfig() != nil {
		autoConf := &httpv3.HttpProtocolOptions_AutoConfig{
			AutoConfig: &httpv3.HttpProtocolOptions_AutoHttpConfig{},
		}
		if protOpt.GetAutoConfig().HttpProtocolOptions != nil {
			autoConf.AutoConfig.HttpProtocolOptions = &corev3.Http1ProtocolOptions{
				EnableTrailers:        protOpt.GetAutoConfig().HttpProtocolOptions.EnableTrailers,
				SendFullyQualifiedUrl: protOpt.GetAutoConfig().HttpProtocolOptions.SendFullyQualifiedUrl,
			}
		}
		if protOpt.GetAutoConfig().Http2ProtocolOptions != nil {
			initStreamWindowsSize := &wrapperspb.UInt32Value{Value: 65536}    // as suggested for envoy as edge proxy
			initConnectWindowsSize := &wrapperspb.UInt32Value{Value: 1048576} // as suggested for envoy as edge proxy
			if protOpt.GetAutoConfig().Http2ProtocolOptions.InitialStreamWindowSize != nil {
				initStreamWindowsSize = protOpt.GetAutoConfig().Http2ProtocolOptions.InitialStreamWindowSize
			}
			if protOpt.GetAutoConfig().Http2ProtocolOptions.InitialConnectionWindowSize != nil {
				initConnectWindowsSize = protOpt.GetAutoConfig().Http2ProtocolOptions.InitialConnectionWindowSize
			}
			autoConf.AutoConfig.Http2ProtocolOptions = &corev3.Http2ProtocolOptions{
				HpackTableSize:                               protOpt.GetAutoConfig().Http2ProtocolOptions.HpackTableSize,
				MaxConcurrentStreams:                         protOpt.GetAutoConfig().Http2ProtocolOptions.MaxConcurrentStreams,
				InitialStreamWindowSize:                      initStreamWindowsSize,
				InitialConnectionWindowSize:                  initConnectWindowsSize,
				AllowConnect:                                 protOpt.GetAutoConfig().Http2ProtocolOptions.AllowConnect,
				MaxOutboundFrames:                            protOpt.GetAutoConfig().Http2ProtocolOptions.MaxOutboundFrames,
				MaxOutboundControlFrames:                     protOpt.GetAutoConfig().Http2ProtocolOptions.MaxOutboundControlFrames,
				MaxConsecutiveInboundFramesWithEmptyPayload:  protOpt.GetAutoConfig().Http2ProtocolOptions.MaxConsecutiveInboundFramesWithEmptyPayload,
				MaxInboundPriorityFramesPerStream:            protOpt.GetAutoConfig().Http2ProtocolOptions.MaxInboundPriorityFramesPerStream,
				MaxInboundWindowUpdateFramesPerDataFrameSent: protOpt.GetAutoConfig().Http2ProtocolOptions.MaxInboundWindowUpdateFramesPerDataFrameSent,
				ConnectionKeepalive:                          coreKeepaliveToEnvoyKeepalive(protOpt.GetAutoConfig().Http2ProtocolOptions.ConnectionKeepalive),
			}
		}
		if protOpt.GetAutoConfig().Http3ProtocolOptions != nil {
			autoConf.AutoConfig.Http3ProtocolOptions = &corev3.Http3ProtocolOptions{
				QuicProtocolOptions:                     quicProtOptToQuicEnvoy(protOpt.GetAutoConfig().Http3ProtocolOptions.QuicProtocolOptions),
				OverrideStreamErrorOnInvalidHttpMessage: protOpt.GetAutoConfig().Http3ProtocolOptions.OverrideStreamErrorOnInvalidHttpMessage,
				AllowExtendedConnect:                    protOpt.GetAutoConfig().Http3ProtocolOptions.AllowExtendedConnect,
			}
		}
		finalOpt.UpstreamProtocolOptions = autoConf
	}
	if protOpt.GetUseDownstreamProtocolConfig() != nil {
		downstreamConf := &httpv3.HttpProtocolOptions_UseDownstreamProtocolConfig{
			UseDownstreamProtocolConfig: &httpv3.HttpProtocolOptions_UseDownstreamHttpConfig{},
		}
		if protOpt.GetUseDownstreamProtocolConfig().HttpProtocolOptions != nil {
			downstreamConf.UseDownstreamProtocolConfig.HttpProtocolOptions = &corev3.Http1ProtocolOptions{
				EnableTrailers:        protOpt.GetUseDownstreamProtocolConfig().HttpProtocolOptions.EnableTrailers,
				SendFullyQualifiedUrl: protOpt.GetUseDownstreamProtocolConfig().HttpProtocolOptions.SendFullyQualifiedUrl,
			}
		}
		if protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions != nil {
			initStreamWindowsSize := &wrapperspb.UInt32Value{Value: 65536}    // as suggested for envoy as edge proxy
			initConnectWindowsSize := &wrapperspb.UInt32Value{Value: 1048576} // as suggested for envoy as edge proxy
			if protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.InitialStreamWindowSize != nil {
				initStreamWindowsSize = protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.InitialStreamWindowSize
			}
			if protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.InitialConnectionWindowSize != nil {
				initConnectWindowsSize = protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.InitialConnectionWindowSize
			}
			downstreamConf.UseDownstreamProtocolConfig.Http2ProtocolOptions = &corev3.Http2ProtocolOptions{
				HpackTableSize:                               protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.HpackTableSize,
				MaxConcurrentStreams:                         protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.MaxConcurrentStreams,
				InitialStreamWindowSize:                      initStreamWindowsSize,
				InitialConnectionWindowSize:                  initConnectWindowsSize,
				AllowConnect:                                 protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.AllowConnect,
				MaxOutboundFrames:                            protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.MaxOutboundFrames,
				MaxOutboundControlFrames:                     protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.MaxOutboundControlFrames,
				MaxConsecutiveInboundFramesWithEmptyPayload:  protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.MaxConsecutiveInboundFramesWithEmptyPayload,
				MaxInboundPriorityFramesPerStream:            protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.MaxInboundPriorityFramesPerStream,
				MaxInboundWindowUpdateFramesPerDataFrameSent: protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.MaxInboundWindowUpdateFramesPerDataFrameSent,
				ConnectionKeepalive:                          coreKeepaliveToEnvoyKeepalive(protOpt.GetUseDownstreamProtocolConfig().Http2ProtocolOptions.ConnectionKeepalive),
			}
		}
		if protOpt.GetUseDownstreamProtocolConfig().Http3ProtocolOptions != nil {
			downstreamConf.UseDownstreamProtocolConfig.Http3ProtocolOptions = &corev3.Http3ProtocolOptions{
				QuicProtocolOptions:                     quicProtOptToQuicEnvoy(protOpt.GetUseDownstreamProtocolConfig().Http3ProtocolOptions.QuicProtocolOptions),
				OverrideStreamErrorOnInvalidHttpMessage: protOpt.GetUseDownstreamProtocolConfig().Http3ProtocolOptions.OverrideStreamErrorOnInvalidHttpMessage,
				AllowExtendedConnect:                    protOpt.GetUseDownstreamProtocolConfig().Http3ProtocolOptions.AllowExtendedConnect,
			}
		}
		finalOpt.UpstreamProtocolOptions = downstreamConf
	}
	return finalOpt
}

func quicProtOptToQuicEnvoy(opt *starling_commopts.QuicProtocolOptions) *corev3.QuicProtocolOptions {
	if opt == nil {
		return nil
	}
	return &corev3.QuicProtocolOptions{
		MaxConcurrentStreams:              opt.MaxConcurrentStreams,
		InitialStreamWindowSize:           opt.InitialStreamWindowSize,
		InitialConnectionWindowSize:       opt.InitialConnectionWindowSize,
		NumTimeoutsToTriggerPortMigration: opt.NumTimeoutsToTriggerPortMigration,
		ConnectionKeepalive:               coreQuicKeepaliveToEnvoyQuicKeepalive(opt.ConnectionKeepalive),
	}
}

func coreKeepaliveToEnvoyKeepalive(keepalive *starling_commopts.KeepaliveSettings) *corev3.KeepaliveSettings {
	if keepalive == nil {
		return nil
	}
	return &corev3.KeepaliveSettings{
		Interval: keepalive.Interval,
		Timeout:  keepalive.Timeout,
	}
}

func coreQuicKeepaliveToEnvoyQuicKeepalive(keepalive *starling_commopts.QuicKeepAliveSettings) *corev3.QuicKeepAliveSettings {
	if keepalive == nil {
		return nil
	}
	return &corev3.QuicKeepAliveSettings{
		MaxInterval:     keepalive.MaxInterval,
		InitialInterval: keepalive.InitialInterval,
	}
}
