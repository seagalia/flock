package adshandlers

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/helpers"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{

	resourceInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "flock_agent",
		Subsystem: "type_stored",
		Name:      "info",
		Help:      "Information about the type of resource stored and used by the agent",
	}, []string{
		"type_url",
		"uuid",
		"name",
		"signature",
	}),

	addResourceSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "ads_handler",
		Name:      "add_resource_success",
		Help:      "Number of resource added successfully on ads handler implementation for resource type",
	}, []string{
		"handler_name",
		"type_url",
	}),
	addResourceFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "ads_handler",
		Name:      "add_resource_failure",
		Help:      "Number of resource failed addition on ads handler implementation for resource type",
	}, []string{
		"handler_name",
		"type_url",
	}),

	rmResourceSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "ads_handler",
		Name:      "remove_resource_success",
		Help:      "Number of resource removed successfully on ads handler implementation for resource type",
	}, []string{
		"handler_name",
		"type_url",
	}),
	rmResourceFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "ads_handler",
		Name:      "remove_resource_failure",
		Help:      "Number of resource failed when remove on ads handler implementation for resource type",
	}, []string{
		"handler_name",
		"type_url",
	}),
}

type metrics struct {
	resourceInfo       *prometheus.GaugeVec
	addResourceSuccess *prometheus.CounterVec
	addResourceFailure *prometheus.CounterVec
	rmResourceSuccess  *prometheus.CounterVec
	rmResourceFailure  *prometheus.CounterVec
}

func init() {
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.resourceInfo)
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.addResourceSuccess)
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.addResourceFailure)
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.rmResourceSuccess)
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, stats.rmResourceFailure)

}

func (m *metrics) AddResource(typeUrl, uuid, name, signature string) {
	// the signature of a secret or a certificate is a fast hash of its values or its private key, so anyone scraping
	// the agent could check a guess offline from the label
	holdsSensitiveValues := typeUrl == helpers.TypeUrl[*secret.Secret]() || typeUrl == helpers.TypeUrl[*cert.Certificate]()
	if holdsSensitiveValues {
		signature = ""
	}
	m.resourceInfo.WithLabelValues(typeUrl, uuid, name, signature).Set(1)
}

func (m *metrics) RemoveResource(typeUrl, uuid, name string) {
	m.resourceInfo.DeletePartialMatch(map[string]string{
		"type_url": typeUrl,
		"uuid":     uuid,
		"name":     name,
	})
}

func (m *metrics) IncAddResourceSuccess(handlerName, typeUrl string) {
	m.addResourceSuccess.WithLabelValues(handlerName, typeUrl).Add(1)
}

func (m *metrics) IncAddResourceFailure(handlerName, typeUrl string) {
	m.addResourceFailure.WithLabelValues(handlerName, typeUrl).Add(1)
}

func (m *metrics) IncRmResourceSuccess(handlerName, typeUrl string) {
	m.rmResourceSuccess.WithLabelValues(handlerName, typeUrl).Add(1)
}

func (m *metrics) IncRmResourceFailure(handlerName, typeUrl string) {
	m.rmResourceFailure.WithLabelValues(handlerName, typeUrl).Add(1)
}
