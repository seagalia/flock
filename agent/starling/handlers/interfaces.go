package handlers

import (
	"context"

	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
)

//go:generate go tool mockgen -destination=../testhelpers/mock_handler_interfaces.go -package=testhelpers --source=interfaces.go

type RouteHandler[T vs.IsRouteConfig] interface {
	CreateRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message T) error
	DeleteRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message T) error
	UpdateRoute(ctx context.Context, aVip *vip.VIP, virtServ *vs.VirtualServer, message T) error
}
