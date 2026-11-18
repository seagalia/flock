package app

import (
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	f5_backendopts "github.com/seagalia/flock/sdk/flock/extensions/backend/f5_options/v1"
	f5_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/f5_options/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// extensionsLoaded are the extensions the F5 agent reads, announced to the controller. It has no VIP announce
// extension: the BIG-IP announces its virtual addresses itself.
var extensionsLoaded = []*core.Extension{
	{
		TypeUrl:  helpers.TypeUrl[*f5_backendopts.BackendOptions](),
		Category: "flock.api.config.backend.v1.backend.typed_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*f5_vsopts.VirtualServerOptions](),
		Category: "flock.api.config.vs.v1.tcp_route_config.typed_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*f5_vsopts.VirtualServerOptions](),
		Category: "flock.api.config.vs.v1.udp_route_config.typed_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*f5_vsopts.VirtualServerOptions](),
		Category: "flock.api.config.vs.v1.tls_tcp_routes_config.typed_options",
	},
	{
		TypeUrl:  helpers.TypeUrl[*f5_vsopts.VirtualServerOptions](),
		Category: "flock.api.config.vs.v1.http_routes_config.typed_options",
	},
}
