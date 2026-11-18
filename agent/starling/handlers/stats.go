package handlers

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{

	addTcpRouteSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "add_tcp_route_success",
		Help:      "Number of tcp route added successfully on virtual server",
	}, []string{
		"type_url",
	}),
	addTcpRouteFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "add_tcp_route_failure",
		Help:      "Number of tcp route failed addition on virtual server",
	}, []string{
		"type_url",
	}),
	rmTcpRouteSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "remove_tcp_route_success",
		Help:      "Number of tcp route removed successfully on virtual server",
	}, []string{
		"type_url",
	}),
	rmTcpRouteFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "remove_tcp_route_failure",
		Help:      "Number of tcp route failed when remove on virtual server",
	}, []string{
		"type_url",
	}),

	addUdpRouteSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "add_udp_route_success",
		Help:      "Number of udp route added successfully on virtual server",
	}, []string{
		"type_url",
	}),
	addUdpRouteFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "add_udp_route_failure",
		Help:      "Number of udp route failed addition on virtual server",
	}, []string{
		"type_url",
	}),
	rmUdpRouteSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "remove_udp_route_success",
		Help:      "Number of udp route removed successfully on virtual server",
	}, []string{
		"type_url",
	}),
	rmUdpRouteFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "remove_udp_route_failure",
		Help:      "Number of udp route failed when remove on virtual server",
	}, []string{
		"type_url",
	}),

	addTlsTcpRouteSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "add_tls_tcp_route_success",
		Help:      "Number of tls tcp route added successfully on virtual server",
	}, []string{
		"type_url",
	}),
	addTlsTcpRouteFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "add_tls_tcp_route_failure",
		Help:      "Number of tls tcp route failed addition on virtual server",
	}, []string{
		"type_url",
	}),
	rmTlsTcpRouteSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "remove_tls_tcp_route_success",
		Help:      "Number of tls tcp route removed successfully on virtual server",
	}, []string{
		"type_url",
	}),
	rmTlsTcpRouteFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "remove_tls_tcp_route_failure",
		Help:      "Number of tls tcp route failed when remove on virtual server",
	}, []string{
		"type_url",
	}),

	addHttpRouteSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "add_http_route_success",
		Help:      "Number of http route added successfully on virtual server",
	}, []string{
		"type_url",
	}),
	addHttpRouteFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "add_http_route_failure",
		Help:      "Number of http route failed addition on virtual server",
	}, []string{
		"type_url",
	}),
	rmHttpRouteSuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "remove_http_route_success",
		Help:      "Number of http route removed successfully on virtual server",
	}, []string{
		"type_url",
	}),
	rmHttpRouteFailure: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "virtual_server",
		Name:      "remove_http_route_failure",
		Help:      "Number of http route failed when remove on virtual server",
	}, []string{
		"type_url",
	}),
}

type metrics struct {
	addTcpRouteSuccess    *prometheus.CounterVec
	addTcpRouteFailure    *prometheus.CounterVec
	rmTcpRouteSuccess     *prometheus.CounterVec
	rmTcpRouteFailure     *prometheus.CounterVec
	addUdpRouteSuccess    *prometheus.CounterVec
	addUdpRouteFailure    *prometheus.CounterVec
	rmUdpRouteSuccess     *prometheus.CounterVec
	rmUdpRouteFailure     *prometheus.CounterVec
	addTlsTcpRouteSuccess *prometheus.CounterVec
	addTlsTcpRouteFailure *prometheus.CounterVec
	rmTlsTcpRouteSuccess  *prometheus.CounterVec
	rmTlsTcpRouteFailure  *prometheus.CounterVec
	addHttpRouteSuccess   *prometheus.CounterVec
	addHttpRouteFailure   *prometheus.CounterVec
	rmHttpRouteSuccess    *prometheus.CounterVec
	rmHttpRouteFailure    *prometheus.CounterVec
}

func init() {
	sdkmetrics.MustRegister(config.AppName, stats.addTcpRouteSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.addTcpRouteFailure)
	sdkmetrics.MustRegister(config.AppName, stats.rmTcpRouteSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.rmTcpRouteFailure)
	sdkmetrics.MustRegister(config.AppName, stats.addUdpRouteSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.addUdpRouteFailure)
	sdkmetrics.MustRegister(config.AppName, stats.rmUdpRouteSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.rmUdpRouteFailure)
	sdkmetrics.MustRegister(config.AppName, stats.addTlsTcpRouteSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.addTlsTcpRouteFailure)
	sdkmetrics.MustRegister(config.AppName, stats.rmTlsTcpRouteSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.rmTlsTcpRouteFailure)
	sdkmetrics.MustRegister(config.AppName, stats.addHttpRouteSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.addHttpRouteFailure)
	sdkmetrics.MustRegister(config.AppName, stats.rmHttpRouteSuccess)
	sdkmetrics.MustRegister(config.AppName, stats.rmHttpRouteFailure)

}
