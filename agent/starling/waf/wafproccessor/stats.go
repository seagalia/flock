package wafproccessor

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/agent/starling/config"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"
)

var stats = metrics{
	requests: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "starling_waf",
		Name:      "requests",
		Help:      "Number of requests checked by the WAF, by result: passed, detected (rules matched, not blocked), blocked or refused (the WAF held as many requests as its memory allows)",
	}, []string{
		"result",
	}),

	ruleMatches: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "starling_waf",
		Name:      "rule_matches",
		Help:      "Number of matches of WAF rules with the log action, by rule id and result of the request",
	}, []string{
		"rule_id",
		"result",
	}),
}

type metrics struct {
	requests    *prometheus.CounterVec
	ruleMatches *prometheus.CounterVec
}

func init() {
	sdkmetrics.MustRegister(config.WafAppName, stats.requests)
	sdkmetrics.MustRegister(config.WafAppName, stats.ruleMatches)
}
