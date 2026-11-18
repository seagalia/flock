package stores

import (
	"context"
	"fmt"
	"time"

	"github.com/avast/retry-go/v5"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
)

// retryDelay is the wait between two reads of a resource the stream may not have delivered yet
const retryDelay = 1 * time.Second

func RetryFindVirtualServer(ctx context.Context, sm storers.StoreManager, uuid string) (*vs.VirtualServer, error) {
	var aVs *vs.VirtualServer
	var err error
	retryErr := retry.New(retry.Context(ctx), retry.Attempts(3), retry.Delay(retryDelay)).Do(func() error {
		aVs, err = sm.VirtualServers().Read(ctx, uuid)
		return err
	})
	if retryErr != nil {
		return nil, fmt.Errorf("failed to retrieve virtual server %s: %w", uuid, retryErr)
	}
	return aVs, nil
}

func RetryFindVip(ctx context.Context, sm storers.StoreManager, uuid string) (*vip.VIP, error) {
	var aVip *vip.VIP
	var err error
	retryErr := retry.New(retry.Context(ctx), retry.Attempts(3), retry.Delay(retryDelay)).Do(func() error {
		aVip, err = sm.VIPs().Read(ctx, uuid)
		return err
	})
	if retryErr != nil {
		return nil, fmt.Errorf("failed to retrieve vip %s: %w", uuid, retryErr)
	}
	return aVip, nil
}

func FindVipsForVirtualServer(ctx context.Context, sm storers.StoreManager, vsUuid string) ([]*vip.VIP, error) {
	allVips, err := sm.VIPs().List(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list vips: %w", err)
	}
	finalVips := make([]*vip.VIP, 0)
	for _, aVip := range allVips {
		for _, virtualServer := range aVip.GetVirtualServersUuid() {
			if virtualServer == vsUuid {
				finalVips = append(finalVips, aVip)
			}
		}
	}
	if len(finalVips) == 0 {
		return nil, fmt.Errorf("no vip found for virtual server %s", vsUuid)
	}
	return finalVips, nil
}

func RetryFindVipsForVirtualServer(ctx context.Context, sm storers.StoreManager, vsUuid string) ([]*vip.VIP, error) {
	var vips []*vip.VIP
	var err error
	retryErr := retry.New(retry.Context(ctx), retry.Attempts(3), retry.Delay(retryDelay)).Do(func() error {
		vips, err = FindVipsForVirtualServer(ctx, sm, vsUuid)
		return err
	})
	if retryErr != nil {
		return nil, fmt.Errorf("failed to retrieve vips for virtual server %s: %w", vsUuid, retryErr)
	}
	return vips, nil
}

func RetryFindBackend(ctx context.Context, sm storers.StoreManager, backendUuid string) (*backend.Backend, error) {
	var aBackend *backend.Backend
	var err error
	retryErr := retry.New(retry.Context(ctx), retry.Attempts(3), retry.Delay(retryDelay)).Do(func() error {
		aBackend, err = sm.Backends().Read(ctx, backendUuid)
		return err
	})
	if retryErr != nil {
		return nil, fmt.Errorf("failed to retrieve backend %s: %w", backendUuid, retryErr)
	}
	return aBackend, nil
}

func FindByTypeAndUuidByStore(ctx context.Context, sm storers.StoreManager, typeUrl, uuid string) (proto.Message, error) {
	switch typeUrl {
	case helpers.TypeUrl[*backend.Backend]():
		return sm.Backends().Read(ctx, uuid)
	case helpers.TypeUrl[*instance.Instance]():
		return sm.Instances().Read(ctx, uuid)
	case helpers.TypeUrl[*vip.VIP]():
		return sm.VIPs().Read(ctx, uuid)
	case helpers.TypeUrl[*vs.VirtualServer]():
		return sm.VirtualServers().Read(ctx, uuid)
	case helpers.TypeUrl[*cert.Certificate]():
		return sm.Certificates().Read(ctx, uuid)
	default:
		return nil, fmt.Errorf("no store found for type %s", typeUrl)
	}
}

func FindAllByTypeForStore(ctx context.Context, sm storers.StoreManager, typeUrl string) ([]proto.Message, error) {
	messages := make([]proto.Message, 0)
	switch typeUrl {
	case helpers.TypeUrl[*backend.Backend]():
		ls, err := sm.Backends().List(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, l := range ls {
			messages = append(messages, l)
		}
		return messages, nil
	case helpers.TypeUrl[*instance.Instance]():
		ls, err := sm.Instances().List(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, l := range ls {
			messages = append(messages, l)
		}
		return messages, nil
	case helpers.TypeUrl[*vip.VIP]():
		ls, err := sm.VIPs().List(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, l := range ls {
			messages = append(messages, l)
		}
		return messages, nil
	case helpers.TypeUrl[*vs.VirtualServer]():
		ls, err := sm.VirtualServers().List(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, l := range ls {
			messages = append(messages, l)
		}
		return messages, nil

	case helpers.TypeUrl[*cert.Certificate]():
		ls, err := sm.Certificates().List(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, l := range ls {
			messages = append(messages, l)
		}
		return messages, nil
	default:
		return nil, fmt.Errorf("no store found for type %s", typeUrl)
	}
}
