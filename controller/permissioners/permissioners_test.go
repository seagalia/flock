package permissioners_test

import (
	"context"

	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/dbs"
	. "github.com/seagalia/flock/controller/permissioners"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type fakePermissioner struct {
	Null
	hasPerm    bool
	hasPermErr error
	calls      []string
}

func (f *fakePermissioner) HasPermission(_ context.Context, resType string, resId string, role permission.Role, username string, groups []string) (bool, error) {
	f.calls = append(f.calls, "HasPermission")
	return f.hasPerm, f.hasPermErr
}

func (f *fakePermissioner) AddPermission(_ context.Context, resType string, resId string, role permission.Role, memberType permission.MemberType, member string) error {
	f.calls = append(f.calls, "AddPermission")
	return nil
}

func (f *fakePermissioner) DeletePermission(_ context.Context, resType string, resId string, memberType permission.MemberType, member string) error {
	f.calls = append(f.calls, "DeletePermission")
	return nil
}

func (f *fakePermissioner) SetAllowAllRead(_ context.Context, resType string, resId string, allowAll bool) error {
	f.calls = append(f.calls, "SetAllowAllRead")
	return nil
}

func (f *fakePermissioner) IsAllowAllRead(_ context.Context, resType string, resId string) (bool, error) {
	f.calls = append(f.calls, "IsAllowAllRead")
	return true, nil
}

func (f *fakePermissioner) ListMemberPermissions(_ context.Context, resType string, resId string, role permission.Role) ([]*permission.MemberPermission, error) {
	f.calls = append(f.calls, "ListMemberPermissions")
	return nil, nil
}

func (f *fakePermissioner) Name() string {
	return "Fake"
}

var _ = Describe("Metrics", func() {
	var fake *fakePermissioner
	var metrics *Metrics
	typeUrl := helpers.TypeUrl[*backend.Backend]()
	BeforeEach(func() {
		fake = &fakePermissioner{}
		metrics = NewMetrics(fake)
	})

	It("should delegate all calls to real permissioner", func() {
		Expect(metrics.AddPermission(context.Background(), typeUrl, "id", permission.Role_OWNER, permission.MemberType_USER, "bob")).To(Succeed())
		Expect(metrics.DeletePermission(context.Background(), typeUrl, "id", permission.MemberType_USER, "bob")).To(Succeed())
		Expect(metrics.SetAllowAllRead(context.Background(), typeUrl, "id", true)).To(Succeed())
		Expect(metrics.IsAllowAllRead(context.Background(), typeUrl, "id")).To(BeTrue())
		_, err := metrics.ListMemberPermissions(context.Background(), typeUrl, "id", -1)
		Expect(err).ToNot(HaveOccurred())
		_, err = metrics.HasPermission(context.Background(), typeUrl, "id", permission.Role_OWNER, "bob", nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(fake.calls).To(Equal([]string{
			"AddPermission", "DeletePermission", "SetAllowAllRead",
			"IsAllowAllRead", "ListMemberPermissions", "HasPermission",
		}))
	})
	It("should give result of real permissioner", func() {
		fake.hasPerm = true
		Expect(metrics.HasPermission(context.Background(), typeUrl, "id", permission.Role_OWNER, "bob", nil)).To(BeTrue())

		fake.hasPerm = false
		Expect(metrics.HasPermission(context.Background(), typeUrl, "id", permission.Role_OWNER, "bob", nil)).To(BeFalse())
	})
	It("should give error of real permissioner as a denial", func() {
		fake.hasPerm = true
		fake.hasPermErr = fmt.Errorf("db down")

		ok, err := metrics.HasPermission(context.Background(), typeUrl, "id", permission.Role_OWNER, "bob", nil)

		Expect(err).To(MatchError("db down"))
		Expect(ok).To(BeFalse())
	})
})

var _ = Describe("DbStorePerm", func() {
	var perm *DbStorePerm
	BeforeEach(func() {
		_, err := dbMan.Exec(context.Background(), dbs.MakeDbQuery("DELETE FROM Permissions", nil))
		Expect(err).ToNot(HaveOccurred())
		perm = NewDbStorePerm(storeMan)
	})

	DescribeTable("should manage permissions of each resource type",
		func(typeUrl string, create func(id string) error) {
			Expect(create("res-1")).To(Succeed())

			Expect(perm.AddPermission(context.Background(), typeUrl, "res-1", permission.Role_WRITER, permission.MemberType_USER, "bob")).To(Succeed())
			Expect(perm.HasPermission(context.Background(), typeUrl, "res-1", permission.Role_WRITER, "bob", nil)).To(BeTrue())
			Expect(perm.ListMemberPermissions(context.Background(), typeUrl, "res-1", -1)).To(HaveLen(1))

			Expect(perm.SetAllowAllRead(context.Background(), typeUrl, "res-1", true)).To(Succeed())
			Expect(perm.IsAllowAllRead(context.Background(), typeUrl, "res-1")).To(BeTrue())

			Expect(perm.DeletePermission(context.Background(), typeUrl, "res-1", permission.MemberType_USER, "bob")).To(Succeed())
			Expect(perm.HasPermission(context.Background(), typeUrl, "res-1", permission.Role_WRITER, "bob", nil)).To(BeFalse())
		},
		Entry("backend", helpers.TypeUrl[*backend.Backend](), func(id string) error {
			return storeMan.Backends().Create(context.Background(), &backend.Backend{UUID: id, Name: id})
		}),
		Entry("instance", helpers.TypeUrl[*instance.Instance](), func(id string) error {
			return storeMan.Instances().Create(context.Background(), &instance.Instance{UUID: id, Name: id})
		}),
		Entry("zone", helpers.TypeUrl[*zone.Zone](), func(id string) error {
			return storeMan.Zones().Create(context.Background(), &zone.Zone{UUID: id, Name: id})
		}),
		Entry("vip", helpers.TypeUrl[*vip.VIP](), func(id string) error {
			return storeMan.VIPs().Create(context.Background(), &vip.VIP{UUID: id, Name: id})
		}),
		Entry("virtual server", helpers.TypeUrl[*vs.VirtualServer](), func(id string) error {
			return storeMan.VirtualServers().Create(context.Background(), &vs.VirtualServer{UUID: id, Name: id})
		}),
		Entry("certificate", helpers.TypeUrl[*cert.Certificate](), func(id string) error {
			return storeMan.Certificates().Create(context.Background(), &cert.Certificate{UUID: id, Name: id})
		}),
	)
	It("should stop a permission check on a cancelled ctx", func() {
		typeUrl := helpers.TypeUrl[*backend.Backend]()
		Expect(perm.AddPermission(context.Background(), typeUrl, "res-1", permission.Role_WRITER, permission.MemberType_USER, "bob")).To(Succeed())
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := perm.HasPermission(cancelled, typeUrl, "res-1", permission.Role_WRITER, "bob", nil)

		Expect(err).To(MatchError(context.Canceled))
	})
	It("should refuse unknown resource type", func() {
		typeUrl := helpers.TypeUrl[*backend.BackendEndpointStats]()
		errMsg := "no store found for type " + typeUrl

		Expect(perm.AddPermission(context.Background(), typeUrl, "id", permission.Role_OWNER, permission.MemberType_USER, "bob")).To(MatchError(errMsg))
		Expect(perm.DeletePermission(context.Background(), typeUrl, "id", permission.MemberType_USER, "bob")).To(MatchError(errMsg))
		Expect(perm.SetAllowAllRead(context.Background(), typeUrl, "id", true)).To(MatchError(errMsg))
		_, err := perm.IsAllowAllRead(context.Background(), typeUrl, "id")
		Expect(err).To(MatchError(errMsg))
		_, err = perm.HasPermission(context.Background(), typeUrl, "id", permission.Role_OWNER, "bob", nil)
		Expect(err).To(MatchError(errMsg))
		_, err = perm.ListMemberPermissions(context.Background(), typeUrl, "id", -1)
		Expect(err).To(MatchError(errMsg))
	})
})
