package backendctl

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"github.com/ArthurHlt/gohc"
	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"net/http"
)

var pluginsHc = make([]*config.PluginHealthCheckConfig, 0)

func SetPluginsHc(plugins []*config.PluginHealthCheckConfig) {
	pluginsHc = plugins
}

func makeHealthCheck(hcDef *core.HealthCheck, tlsServerName, rootCa string) (gohc.HealthChecker, error) {
	var hchecker gohc.HealthChecker
	tlsEnable := hcDef.EnableTls
	tlsConf := makeTlsConfig(tlsEnable, tlsServerName, rootCa)
	switch hcDef.GetHealthChecker().(type) {
	case *core.HealthCheck_GrpcHealthCheck:
		hchecker = gohc.NewGrpcHealthCheck(&gohc.GrpcOpt{
			ServiceName: hcDef.GetGrpcHealthCheck().GetServiceName(),
			Authority:   hcDef.GetGrpcHealthCheck().GetAuthority(),
			Timeout:     hcDef.GetTimeout().AsDuration(),
			TlsEnabled:  tlsEnable,
			TlsConfig:   tlsConf,
			AltPort:     hcDef.GetAltPort(),
		})
	case *core.HealthCheck_HttpHealthCheck:
		hcMethod := http.MethodGet
		if hcDef.GetHttpHealthCheck().GetMethod() != core.RequestMethod_METHOD_UNSPECIFIED {
			hcMethod = core.RequestMethod_name[int32(hcDef.GetHttpHealthCheck().GetMethod())]
		}
		hchecker = gohc.NewHttpHealthCheck(&gohc.HttpOpt{
			Host:             hcDef.GetHttpHealthCheck().GetHost(),
			Path:             hcDef.GetHttpHealthCheck().GetPath(),
			Send:             makeGohcPayload(hcDef.GetHttpHealthCheck().GetSend()),
			Receive:          makeGohcPayload(hcDef.GetHttpHealthCheck().GetReceive()),
			Headers:          makeGohcHeader(hcDef.GetHttpHealthCheck().GetRequestHeadersToAdd()),
			ExpectedStatuses: makeGohcExpectedStatus(hcDef.GetHttpHealthCheck().GetExpectedStatuses()),
			CodecClientType:  gohc.CodecClientType(hcDef.GetHttpHealthCheck().GetCodecClientType()),
			Method:           hcMethod,
			Timeout:          hcDef.GetTimeout().AsDuration(),
			TlsEnabled:       tlsEnable,
			TlsConfig:        tlsConf,
			AltPort:          hcDef.GetAltPort(),
		})
	case *core.HealthCheck_TcpHealthCheck:
		hchecker = gohc.NewTcpHealthCheck(&gohc.TcpOpt{
			Send:       makeGohcPayload(hcDef.GetTcpHealthCheck().GetSend()),
			Receive:    makeGohcPayloads(hcDef.GetTcpHealthCheck().GetReceive()),
			Timeout:    hcDef.GetTimeout().AsDuration(),
			TlsEnabled: tlsEnable,
			TlsConfig:  tlsConf,
			AltPort:    hcDef.GetAltPort(),
		})
	case *core.HealthCheck_UdpHealthCheck:
		hchecker = gohc.NewUdpHealthCheck(&gohc.UdpOpt{
			Send:        makeGohcPayload(hcDef.GetUdpHealthCheck().GetSend()),
			Receive:     makeGohcPayloads(hcDef.GetUdpHealthCheck().GetReceive()),
			PingTimeout: hcDef.GetUdpHealthCheck().GetPingTimeout().AsDuration(),
			Timeout:     hcDef.GetTimeout().AsDuration(),
			Delay:       hcDef.GetUdpHealthCheck().GetDelay().AsDuration(),
			AltPort:     hcDef.GetAltPort(),
		})
	case *core.HealthCheck_NoHealthCheck:
		hchecker = gohc.NewNoHealthCheck()
	case *core.HealthCheck_PluginHealthCheck:
		var foundPlugin *config.PluginHealthCheckConfig
		for _, plugin := range pluginsHc {
			if hcDef.GetPluginHealthCheck().Name == plugin.Name {
				foundPlugin = plugin
				break
			}
		}
		if foundPlugin == nil {
			return nil, fmt.Errorf("plugin %s not found", hcDef.GetPluginHealthCheck().GetName())
		}
		cas := make([]string, 0)
		if rootCa != "" {
			cas = append(cas, rootCa)
		}
		hchecker = gohc.NewProgramHealthCheck(&gohc.ProgramOpt{
			Path:       foundPlugin.Path,
			Args:       foundPlugin.Args,
			Options:    hcDef.GetPluginHealthCheck().GetOptions().AsMap(),
			Timeout:    hcDef.GetTimeout().AsDuration(),
			AltPort:    hcDef.GetAltPort(),
			TlsEnabled: tlsEnable,
			ProgramTlsConfig: &gohc.ProgramTlsOpt{
				InsecureSkipVerify: false,
				ServerName:         tlsServerName,
				RootCAs:            cas,
			},
		})
	}
	return hchecker, nil
}

func makeGohcPayload(hcPayload *core.HealthCheckPayload) *gohc.Payload {
	if hcPayload == nil {
		return nil
	}
	return &gohc.Payload{
		Text:   hcPayload.GetText(),
		Binary: hcPayload.GetBinary(),
	}
}

func makeGohcHeader(hcHeaders []*core.HeaderValueOption) http.Header {
	headers := make(http.Header)
	for _, hcHeader := range hcHeaders {
		if !hcHeader.GetAppend() {
			headers.Set(hcHeader.GetHeader().GetKey(), hcHeader.GetHeader().GetValue())
			continue
		}
		headers.Add(hcHeader.GetHeader().GetKey(), hcHeader.GetHeader().GetValue())
	}
	return headers
}

func makeGohcPayloads(hcPayloads []*core.HealthCheckPayload) []*gohc.Payload {
	if len(hcPayloads) == 0 {
		return nil
	}
	payloads := make([]*gohc.Payload, len(hcPayloads))
	for i, hcPayload := range hcPayloads {
		payloads[i] = makeGohcPayload(hcPayload)
	}
	return payloads
}

func makeGohcExpectedStatus(rn *flocktype.Int64Range) *gohc.IntRange {
	if rn == nil {
		return nil
	}
	return &gohc.IntRange{
		Start: rn.GetStart(),
		End:   rn.GetEnd(),
	}
}

func makeTlsConfig(tlsEnable bool, tlsServerName, rootCa string) *tls.Config {
	if !tlsEnable {
		return nil
	}
	var caCertPool *x509.CertPool
	if rootCa != "" {
		caCertPool = x509.NewCertPool()
		caCertPool.AppendCertsFromPEM([]byte(rootCa))
	}
	return &tls.Config{
		RootCAs:    caCertPool,
		ServerName: tlsServerName,
	}
}
