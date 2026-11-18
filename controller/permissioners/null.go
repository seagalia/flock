package permissioners

import (
	"context"

	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
)

type Null struct{}

func NewNull() *Null {
	return &Null{}
}

func (Null) AddPermission(_ context.Context, resType string, resId string, role permission.Role, memberType permission.MemberType, member string) error {
	return nil
}

func (Null) DeletePermission(_ context.Context, resType string, resId string, memberType permission.MemberType, member string) error {
	return nil
}

func (Null) SetAllowAllRead(_ context.Context, resType string, resId string, allowAll bool) error {
	return nil
}

func (Null) IsAllowAllRead(_ context.Context, resType string, resId string) (bool, error) {
	return false, nil
}

func (Null) HasPermission(_ context.Context, resType string, resId string, role permission.Role, username string, groups []string) (bool, error) {
	return true, nil
}

func (Null) ListMemberPermissions(_ context.Context, resType string, resId string, role permission.Role) ([]*permission.MemberPermission, error) {
	return []*permission.MemberPermission{}, nil
}

func (Null) Name() string {
	return "Null"
}
