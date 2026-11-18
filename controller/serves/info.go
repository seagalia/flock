package serves

import (
	"context"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *FlockServer) ListResourceTypes(ctx context.Context, _ *emptypb.Empty) (*svccore.ListResourceTypesResponse, error) {
	return &svccore.ListResourceTypesResponse{
		ResourceTypes: []string{
			helpers.TypeUrl[*backend.Backend](),
			helpers.TypeUrl[*instance.Instance](),
			helpers.TypeUrl[*vip.VIP](),
			helpers.TypeUrl[*vs.VirtualServer](),
			helpers.TypeUrl[*cert.Certificate](),
		},
	}, nil
}

func (s *FlockServer) GetControllerFeatures(_ context.Context, _ *emptypb.Empty) (*svccore.ControllerFeatures, error) {
	return &svccore.ControllerFeatures{IpamEnabled: s.ipam != nil}, nil
}
