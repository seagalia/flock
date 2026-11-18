package handlers

import (
	"context"

	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
)

//go:generate go tool mockgen -destination=../testhelpers/mock_announce_interfaces.go -package=testhelpers --source=announce_interfaces.go

// AttachDetachHandler attaches a virtual server to one or more vips
type AttachDetachHandler interface {
	AttachVip(ctx context.Context, aVip *vip.VIP, vsUUID string) error
	DetachVip(ctx context.Context, aVip *vip.VIP, vsUUID string) error
}

type BgpRegisterer interface {
	AddVip(ctx context.Context, aVip *vip.VIP) error
	DelVip(ctx context.Context, aVip *vip.VIP) error
}
