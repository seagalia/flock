package permissioners

import (
	"context"

	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
)

type Metrics struct {
	real Permissioner
}

func NewMetrics(real Permissioner) *Metrics {
	return &Metrics{real: real}
}

func (m *Metrics) AddPermission(ctx context.Context, resType string, resId string, role permission.Role, memberType permission.MemberType, member string) error {
	return m.real.AddPermission(ctx, resType, resId, role, memberType, member)
}

func (m *Metrics) DeletePermission(ctx context.Context, resType string, resId string, memberType permission.MemberType, member string) error {
	return m.real.DeletePermission(ctx, resType, resId, memberType, member)
}

func (m *Metrics) SetAllowAllRead(ctx context.Context, resType string, resId string, allowAll bool) error {
	return m.real.SetAllowAllRead(ctx, resType, resId, allowAll)
}

func (m *Metrics) IsAllowAllRead(ctx context.Context, resType string, resId string) (bool, error) {
	return m.real.IsAllowAllRead(ctx, resType, resId)
}

func (m *Metrics) HasPermission(ctx context.Context, resType string, resId string, role permission.Role, username string, groups []string) (bool, error) {
	ok, err := m.real.HasPermission(ctx, resType, resId, role, username, groups)
	if err != nil {
		return false, err
	}
	if ok {
		stats.authSuccess.WithLabelValues(username, resType, resId).Inc()
	} else {
		stats.authFail.WithLabelValues(username, resType, resId).Inc()
	}
	return ok, nil
}

func (m *Metrics) ListMemberPermissions(ctx context.Context, resType string, resId string, role permission.Role) ([]*permission.MemberPermission, error) {
	return m.real.ListMemberPermissions(ctx, resType, resId, role)
}

func (m *Metrics) Name() string {
	return "Metrics -> " + m.real.Name()
}
