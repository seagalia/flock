package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	starling_limit "github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_route "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("extensionsLoaded", func() {
	DescribeTable("should declare every extension the agent handles",
		func(typeUrl string, category string) {
			Expect(extensionsLoaded).To(ContainElement(
				And(HaveField("TypeUrl", typeUrl), HaveField("Category", category)),
			))
		},
		// The options of one TlsTcpRouteConfig are not applied by the agent, only those of TlsTcpRoutesConfig.
		Entry("tls tcp options of the routes", helpers.TypeUrl[*starling_vsopts.TlsTcpOptions](),
			"flock.api.config.vs.v1.tls_tcp_routes_config.typed_options"),
		Entry("options of an http route", helpers.TypeUrl[*starling_route.VirtualHostRouteOptions](),
			"flock.api.config.route.v1.route.typed_options"),
		Entry("rate limit", helpers.TypeUrl[*starling_limit.StarlingRateLimit](),
			"flock.api.config.limit.v1.config.typed_limit"),
	)
})
