package permissioners

import (
	"context"

	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
)

type Permissioner interface {
	// AddPermission adds a permission to a resource
	AddPermission(
		ctx context.Context, resType string, resId string, role permission.Role,
		memberType permission.MemberType, member string,
	) error
	// DeletePermission removes a permission from a resource
	DeletePermission(
		ctx context.Context, resType string, resId string,
		memberType permission.MemberType, member string,
	) error
	// SetAllowAllRead sets the allow all read flag on a resource
	SetAllowAllRead(ctx context.Context, resType string, resId string, allowAll bool) error
	// IsAllowAllRead returns true if the allow all read flag is set on a resource
	IsAllowAllRead(ctx context.Context, resType string, resId string) (bool, error)
	// HasPermission is true when the user has role, or a higher one, on the resource
	HasPermission(
		ctx context.Context, resType string, resId string, role permission.Role,
		username string, groups []string,
	) (bool, error)

	// ListMemberPermissions lists the permissions of a resource, of every role when role is -1
	ListMemberPermissions(ctx context.Context, resType string, resId string, role permission.Role) ([]*permission.MemberPermission, error)

	Name() string
}
