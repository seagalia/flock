package serves_test

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/contexes"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svclease "github.com/seagalia/flock/sdk/flock/services/lease/v1"
	svcperm "github.com/seagalia/flock/sdk/flock/services/permission/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/types/known/durationpb"
)

var _ = Describe("Lease", func() {
	var server *testServer
	var instanceUuid, certUuid string
	certType := helpers.TypeUrl[*cert.Certificate]()

	// leaseCtx is the call of the agent named cn, authenticated by its client certificate
	leaseCtx := func(cn string) context.Context {
		return contexes.WithUserInfo(agentContext(cn), cn, []string{"agent-" + cn})
	}
	grantWriter := func(cn string) {
		_, err := server.AddPermission(userCtx(adminUser), &svcperm.AddPermissionRequest{
			ResourceType: certType, ResourceUuid: certUuid, Role: permission.Role_WRITER,
			Member: &permission.Member{MemberType: permission.MemberType_GROUP, MemberName: "agent-" + cn},
		})
		Expect(err).ToNot(HaveOccurred())
	}
	acquire := func(ctx context.Context, ttl *durationpb.Duration) (*svclease.AcquireResponse, error) {
		return server.Acquire(ctx, &svclease.AcquireRequest{
			TypeUrl: certType, UUID: certUuid, InstanceUuid: instanceUuid, Ttl: ttl,
		})
	}

	BeforeEach(func() {
		instanceUuid = "instance-1"
		inst := newFakeInstantiater()
		inst.agents = []*agent.Agent{
			{UUID: "uuid-1", Name: "prov-1"},
			{UUID: "uuid-2", Name: "prov-2"},
			{UUID: "uuid-3", Name: "prov-3"},
		}
		server = newTestServer(inst)
		certPem, keyPem := generateCertPair()
		created, err := server.CreateCertificate(userCtx(adminUser), &svccert.CreateCertificateRequest{Certificate: &cert.Certificate{
			Name: "cert-1", CertChain: "\n" + certPem + "\n", PrivateKey: keyPem, Ca: certPem,
		}})
		Expect(err).ToNot(HaveOccurred())
		certUuid = created.GetCertificate().GetUUID()
		grantWriter("prov-1")
		grantWriter("prov-2")
	})

	It("grants the lease to a declared agent with the writer role", func() {
		resp, err := acquire(leaseCtx("prov-1"), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.GetGranted()).To(BeTrue())
		Expect(resp.GetLeaseId()).ToNot(BeEmpty())
		Expect(resp.GetHolderUuid()).To(Equal("uuid-1"))
		Expect(resp.GetHolderName()).To(Equal("prov-1"))
	})

	It("answers the holder to another replica", func() {
		_, err := acquire(leaseCtx("prov-1"), nil)
		Expect(err).ToNot(HaveOccurred())
		resp, err := acquire(leaseCtx("prov-2"), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.GetGranted()).To(BeFalse())
		Expect(resp.GetLeaseId()).To(BeEmpty())
		Expect(resp.GetHolderName()).To(Equal("prov-1"))
	})

	It("refuses a caller without the writer role", func() {
		_, err := acquire(leaseCtx("prov-3"), nil)
		expectCode(err, connect.CodePermissionDenied)
	})

	It("refuses a CN not declared on the instance", func() {
		grantWriter("prov-9")
		_, err := acquire(leaseCtx("prov-9"), nil)
		expectCode(err, connect.CodePermissionDenied)
	})

	It("refuses a user with no client certificate", func() {
		_, err := server.AddPermission(userCtx(adminUser), &svcperm.AddPermissionRequest{
			ResourceType: certType, ResourceUuid: certUuid, Role: permission.Role_WRITER,
			Member: &permission.Member{MemberType: permission.MemberType_USER, MemberName: "bob"},
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = acquire(userCtx("bob"), nil)
		expectCode(err, connect.CodePermissionDenied)
	})

	DescribeTable("clamps the ttl",
		func(ttl *durationpb.Duration, expected time.Duration) {
			resp, err := acquire(leaseCtx("prov-1"), ttl)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetExpiresAt().AsTime()).To(BeTemporally("~", time.Now().Add(expected), 3*time.Second))
		},
		Entry("up to 10s", durationpb.New(time.Second), 10*time.Second),
		Entry("down to 5m", durationpb.New(time.Hour), 5*time.Minute),
		Entry("to 1m when unset", nil, time.Minute),
	)

	It("renews a held lease", func() {
		granted, err := acquire(leaseCtx("prov-1"), durationpb.New(10*time.Second))
		Expect(err).ToNot(HaveOccurred())
		renewed, err := server.Renew(leaseCtx("prov-1"), &svclease.RenewRequest{
			LeaseId: granted.GetLeaseId(), Ttl: durationpb.New(5 * time.Minute),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(renewed.GetExpiresAt().AsTime()).To(BeTemporally(">", granted.GetExpiresAt().AsTime()))
	})

	It("answers NotFound on the renewal of a lost lease", func() {
		_, err := server.Renew(leaseCtx("prov-1"), &svclease.RenewRequest{LeaseId: "6f1c2d6e-0b7a-4c8e-9d55-1f0a3c1b7e11"})
		expectCode(err, connect.CodeNotFound)
	})

	It("answers NotFound on the renewal by another CN", func() {
		granted, err := acquire(leaseCtx("prov-1"), nil)
		Expect(err).ToNot(HaveOccurred())
		_, err = server.Renew(leaseCtx("prov-2"), &svclease.RenewRequest{LeaseId: granted.GetLeaseId()})
		expectCode(err, connect.CodeNotFound)
	})

	It("refuses the renewal with no client certificate", func() {
		_, err := server.Renew(userCtx("bob"), &svclease.RenewRequest{LeaseId: "6f1c2d6e-0b7a-4c8e-9d55-1f0a3c1b7e11"})
		expectCode(err, connect.CodePermissionDenied)
	})

	It("refuses the release by another CN", func() {
		granted, err := acquire(leaseCtx("prov-1"), nil)
		Expect(err).ToNot(HaveOccurred())
		_, err = server.Release(leaseCtx("prov-2"), &svclease.ReleaseRequest{LeaseId: granted.GetLeaseId()})
		Expect(err).ToNot(HaveOccurred())
		resp, err := acquire(leaseCtx("prov-2"), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.GetGranted()).To(BeFalse())
	})

	It("releases twice with no error", func() {
		granted, err := acquire(leaseCtx("prov-1"), nil)
		Expect(err).ToNot(HaveOccurred())
		for range 2 {
			_, err = server.Release(leaseCtx("prov-1"), &svclease.ReleaseRequest{LeaseId: granted.GetLeaseId()})
			Expect(err).ToNot(HaveOccurred())
		}
		resp, err := acquire(leaseCtx("prov-2"), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.GetGranted()).To(BeTrue())
	})
})
