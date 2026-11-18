package app

import (
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	starling_backendopts "github.com/seagalia/flock/sdk/flock/extensions/backend/starling_options/v1"
	starling_commopts "github.com/seagalia/flock/sdk/flock/extensions/common/starling_options/v1"
	starling_limit "github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_route "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var extensionsLoaded = []*core.Extension{
	{
		TypeUrl:  helpers.TypeUrl[*starling_announce.StarlingAnnounce](),
		Category: "flock.api.config.vip.v1.vip.typed_announce",
	},
	{
		TypeUrl:  helpers.TypeUrl[*starling_backendopts.BackendOptions](),
		Category: "flock.api.config.backend.v1.backend.typed_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*starling_commopts.HttpProtocolOptions](),
		Category: "flock.api.config.backend.v1.backend.http_protocol_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*starling_vsopts.TcpUdpOptions](),
		Category: "flock.api.config.vs.v1.tcp_route_config.typed_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*starling_vsopts.TcpUdpOptions](),
		Category: "flock.api.config.vs.v1.udp_route_config.typed_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*starling_vsopts.TlsTcpOptions](),
		Category: "flock.api.config.vs.v1.tls_tcp_routes_config.typed_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*starling_vsopts.HttpOptions](),
		Category: "flock.api.config.vs.v1.http_routes_config.typed_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*starling_route.VirtualHostRouteOptions](),
		Category: "flock.api.config.route.v1.route.typed_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*starling_limit.StarlingRateLimit](),
		Category: "flock.api.config.limit.v1.config.typed_limit",
	},
}
