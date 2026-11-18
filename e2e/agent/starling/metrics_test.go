//go:build e2e

package e2e_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// the agent adds labels to every metric it serves, the expressions accept any label next to the ones they check
var _ = Describe("Prometheus metrics", func() {
	It("serves envoy, agent and rpc metrics", func(ctx SpecContext) {
		envoyXds := `grpc_method="DeltaAggregatedResources"[^}]*grpc_service="envoy.service.discovery.v3.AggregatedDiscoveryService"[^}]*grpc_type="bidi_stream"`
		Eventually(func() string { return main.Metrics(ctx) }).Should(And(
			MatchRegexp(`envoy_server_live\{[^}]*\} 1`),
			MatchRegexp(`flock_agent_status\{[^}]*\} `),
			// envoy subscribes to its configuration on the xDS stream the agent serves
			MatchRegexp(`grpc_server_started_total\{[^}]*`+envoyXds+`[^}]*\} [1-9]`),
			MatchRegexp(`grpc_server_msg_received_total\{[^}]*`+envoyXds+`[^}]*\} [1-9]`),
			// the connect services are exposed before their first call
			MatchRegexp(`grpc_server_started_total\{[^}]*grpc_method="GetAgentStatus"[^}]*`+
				`grpc_service="flock.services.agent.status.v1.AgentStatus"[^}]*grpc_type="unary"[^}]*\} \d+`),
		))
	})
})
