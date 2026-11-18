package agentserv

import (
	"context"
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
)

const (
	collectTimeout = 30 * time.Second
)

func (s *AgentServer) Describe(descs chan<- *prometheus.Desc) {
	descs <- s.statusDesc
	descs <- s.compStatusDesc
	descs <- s.announceDesc
}

func (s *AgentServer) Collect(metrics chan<- prometheus.Metric) {
	envoyStatus := componentState("envoy", s.isEnvoyAlive).GetStatus()
	wafStatus := componentState("waf", s.isWafAlive).GetStatus()

	// the agent can't serve its WAF routes without the WAF, so it is as down as without envoy
	agentStatus := agent.Status_OK
	isComponentDown := envoyStatus == agent.Status_ERRORED || wafStatus == agent.Status_ERRORED
	if isComponentDown {
		agentStatus = agent.Status_ERRORED
	}
	metrics <- prometheus.MustNewConstMetric(s.statusDesc, prometheus.GaugeValue, float64(agentStatus), s.name)
	metrics <- prometheus.MustNewConstMetric(s.compStatusDesc, prometheus.GaugeValue, float64(envoyStatus), "envoy")
	metrics <- prometheus.MustNewConstMetric(s.compStatusDesc, prometheus.GaugeValue, float64(wafStatus), "waf")
	metrics <- prometheus.MustNewConstMetric(s.compStatusDesc, prometheus.GaugeValue, float64(agent.Status_OK), "ipvs")

	var announcingVips []*vip.VIP
	var err error
	// Prometheus calls Collect with no ctx, the scrape is the root of its store read
	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()
	announcingVips, err = s.listAnnouncingVips(ctx)
	if err != nil {
		return
	}
	for _, v := range announcingVips {
		metrics <- prometheus.MustNewConstMetric(
			s.announceDesc, prometheus.GaugeValue, 1,
			v.GetUUID(),
			v.GetIp(),
			fmt.Sprintf("%d", v.GetPrefixLength()),
		)
	}
}
