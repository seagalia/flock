package permissioners

import (
	"context"
	"fmt"

	"github.com/seagalia/flock/controller/dbstore"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type DbStorePerm struct {
	man *dbstore.DbStoreManager
}

type dbStorePerm interface {
	HasPermission(
		ctx context.Context, resId string, role permission.Role,
		username string, groups []string,
	) (bool, error)
	SetAllowAllRead(ctx context.Context, resId string, allowAll bool) error

	IsAllowAllRead(ctx context.Context, resId string) (bool, error)
	DeletePermission(
		ctx context.Context, resId string,
		memberType permission.MemberType, member string,
	) error
	AddPermission(
		ctx context.Context, resId string, role permission.Role,
		memberType permission.MemberType, member string,
	) error
	ListMemberPermissions(ctx context.Context, resId string, role permission.Role) ([]*permission.MemberPermission, error)
}

func NewDbStorePerm(man *dbstore.DbStoreManager) *DbStorePerm {
	return &DbStorePerm{
		man: man,
	}
}

func (p *DbStorePerm) AddPermission(ctx context.Context, resType string, resId string, role permission.Role, memberType permission.MemberType, member string) error {
	store, err := p.findStore(resType)
	if err != nil {
		return err
	}
	return store.AddPermission(ctx, resId, role, memberType, member)
}

func (p *DbStorePerm) DeletePermission(ctx context.Context, resType string, resId string, memberType permission.MemberType, member string) error {
	store, err := p.findStore(resType)
	if err != nil {
		return err
	}
	return store.DeletePermission(ctx, resId, memberType, member)
}

func (p *DbStorePerm) SetAllowAllRead(ctx context.Context, resType string, resId string, allowAll bool) error {
	store, err := p.findStore(resType)
	if err != nil {
		return err
	}
	return store.SetAllowAllRead(ctx, resId, allowAll)
}

func (p *DbStorePerm) HasPermission(ctx context.Context, resType string, resId string, role permission.Role, username string, groups []string) (bool, error) {
	store, err := p.findStore(resType)
	if err != nil {
		return false, err
	}
	return store.HasPermission(ctx, resId, role, username, groups)
}

func (p *DbStorePerm) ListMemberPermissions(ctx context.Context, resType string, resId string, role permission.Role) ([]*permission.MemberPermission, error) {
	store, err := p.findStore(resType)
	if err != nil {
		return nil, err
	}
	return store.ListMemberPermissions(ctx, resId, role)
}

func (p *DbStorePerm) IsAllowAllRead(ctx context.Context, resType string, resId string) (bool, error) {
	store, err := p.findStore(resType)
	if err != nil {
		return false, err
	}
	return store.IsAllowAllRead(ctx, resId)
}

func (p *DbStorePerm) Name() string {
	return "DbStore"
}

func (p *DbStorePerm) findStore(resType string) (dbStorePerm, error) {
	switch resType {
	case helpers.TypeUrl[*backend.Backend]():
		return p.man.Backends().(*dbstore.DbStore[*backend.Backend]), nil
	case helpers.TypeUrl[*instance.Instance]():
		return p.man.Instances().(*dbstore.DbStore[*instance.Instance]), nil
	case helpers.TypeUrl[*vip.VIP]():
		return p.man.VIPs().(*dbstore.DbStore[*vip.VIP]), nil
	case helpers.TypeUrl[*vs.VirtualServer]():
		return p.man.VirtualServers().(*dbstore.DbStore[*vs.VirtualServer]), nil
	case helpers.TypeUrl[*cert.Certificate]():
		return p.man.Certificates().(*dbstore.DbStore[*cert.Certificate]), nil
	case helpers.TypeUrl[*secret.Secret]():
		return p.man.Secrets().(*dbstore.DbStore[*secret.Secret]), nil
	case helpers.TypeUrl[*zone.Zone]():
		return p.man.Zones().(*dbstore.DbStore[*zone.Zone]), nil
	default:
		return nil, fmt.Errorf("no store found for type %s", resType)
	}
}
