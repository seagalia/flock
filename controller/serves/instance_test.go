package serves_test

import (
	"context"

	"fmt"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	corev1 "github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type fakeInstantiater struct {
	instantiaters.Null
	status     *instance.InstanceStatus
	canErr     error
	cannot     bool
	deleteErr  error
	onDelete   func()
	agents     []*agent.Agent
	lastParams instantiaters.InstanceParams
}

func newFakeInstantiater() *fakeInstantiater {
	return &fakeInstantiater{
		status: &instance.InstanceStatus{
			CurrentState: instance.InstanceStatus_RUNNING,
			DesiredState: instance.InstanceStatus_RUNNING,
		},
	}
}

func (f *fakeInstantiater) CanInstantiate(_ context.Context, params instantiaters.InstanceParams) (bool, error) {
	if f.canErr != nil {
		return false, f.canErr
	}
	return !f.cannot, nil
}

func (f *fakeInstantiater) CreateServiceInstance(_ context.Context, params instantiaters.InstanceParams) (*instance.InstanceStatus, error) {
	f.lastParams = params
	return f.status, nil
}

func (f *fakeInstantiater) UpdateServiceInstance(_ context.Context, params instantiaters.InstanceParams) (*instance.InstanceStatus, error) {
	f.lastParams = params
	return f.status, nil
}

func (f *fakeInstantiater) DeleteServiceInstance(_ context.Context, instanceUuid string) (*instance.InstanceStatus, error) {
	if f.onDelete != nil {
		f.onDelete()
	}
	return f.status, f.deleteErr
}

func (f *fakeInstantiater) GetStatus(_ context.Context, instanceUuid string) (*instance.InstanceStatus, error) {
	return f.status, nil
}

func (f *fakeInstantiater) ListAgents(_ context.Context, instanceUuid string) ([]*agent.Agent, error) {
	return f.agents, nil
}

var _ = Describe("Instance", func() {
	var server *testServer
	var inst *fakeInstantiater
	var zoneUuid string
	instanceType := helpers.TypeUrl[*instance.Instance]()
	BeforeEach(func() {
		inst = newFakeInstantiater()
		server = newTestServer(inst)
		zoneUuid = server.createZone("zone-1")
	})
	// createVip gives a vip bob can read, as an admin would grant it
	createVip := func(name string) string {
		resp, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip(name)})
		Expect(err).ToNot(HaveOccurred())
		vipUuid := resp.GetVip().GetUUID()
		Expect(server.perm.AddPermission(context.Background(), helpers.TypeUrl[*vip.VIP](), vipUuid, permission.Role_READER, permission.MemberType_USER, "bob")).To(Succeed())
		return vipUuid
	}

	Context("CreateInstance", func() {
		It("should instantiate and store instance owned by user", func() {
			ctx := withHeaders(userCtx("bob"), "originating", "a", "originating", "b")

			resp, err := server.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})
			Expect(err).ToNot(HaveOccurred())
			instUuid := resp.GetInstance().GetUUID()

			Expect(inst.lastParams.InstanceUUID).To(Equal(instUuid))
			Expect(inst.lastParams.ResourceTypeName).To(Equal("small"))
			Expect(inst.lastParams.OriginatingIdentity).To(Equal("a,b"))
			Expect(dbStoreMan.Instances().Exists(context.Background(), instUuid)).To(BeTrue())
			Expect(server.perm.HasPermission(context.Background(), instanceType, instUuid, permission.Role_OWNER, "bob", nil)).To(BeTrue())
		})
		It("should give the zone name to the instantiater", func() {
			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})
			Expect(err).ToNot(HaveOccurred())
			Expect(inst.lastParams.Zone).To(Equal("zone-1"))
		})
		It("answers AlreadyExists for a name already taken", func() {
			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})

			expectCode(err, connect.CodeAlreadyExists)
		})
		It("should refuse unknown zone", func() {
			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", "unknown")})
			expectCode(err, connect.CodeNotFound)
		})
		It("should refuse when instantiater cannot instantiate the resource type", func() {
			inst.cannot = true

			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})

			expectCode(err, connect.CodeFailedPrecondition)
		})
		It("should refuse when instantiater cannot instantiate", func() {
			inst.canErr = fmt.Errorf("resource type not supported")

			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})

			expectCode(err, connect.CodeInternal)
			Expect(err).To(MatchError(ContainSubstring("resource type not supported")))
		})
		It("should refuse unknown vip", func() {
			_, err := server.CreateInstance(userCtx(adminUser), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, "unknown")})
			expectCode(err, connect.CodeNotFound)
		})
		It("should refuse vip already used by another instance", func() {
			vipUuid := createVip("vip-1")
			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, vipUuid)})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-2", zoneUuid, vipUuid)})

			expectCode(err, connect.CodeAlreadyExists)
		})
		It("should refuse a vip the user cannot read", func() {
			resp, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-other")})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, resp.GetVip().GetUUID())})

			expectCode(err, connect.CodePermissionDenied)
		})
		It("should refuse twice the same vip", func() {
			vipUuid := createVip("vip-1")
			client := svcinstanceconnect.NewInstanceClient(server.throughChain())

			_, err := client.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, vipUuid, vipUuid)})

			expectCode(err, connect.CodeInvalidArgument)
		})
		It("should not give the owner of an instance permissions on its zone", func() {
			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})
			Expect(err).ToNot(HaveOccurred())

			Consistently(func() (bool, error) {
				return server.perm.HasPermission(context.Background(), helpers.TypeUrl[*zone.Zone](), zoneUuid, permission.Role_READER, "bob", nil)
			}, 300*time.Millisecond).Should(BeFalse())
		})
		It("should accept a vip already used by an instance of another zone", func() {
			vipUuid := createVip("vip-1")
			otherZone := server.createZone("zone-2")
			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, vipUuid)})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-2", otherZone, vipUuid)})

			Expect(err).ToNot(HaveOccurred())
		})
		It("should not store instance when instantiater gives errored state", func() {
			inst.status = &instance.InstanceStatus{CurrentState: instance.InstanceStatus_ERRORED, ErrorMessage: "no capacity"}

			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})

			expectCode(err, connect.CodeInternal)
			Expect(err).To(MatchError(ContainSubstring("no capacity")))
			Expect(dbStoreMan.Instances().List(context.Background(), nil)).To(BeEmpty())
		})
	})

	Context("existing instance", func() {
		var instUuid, vipUuid string
		BeforeEach(func() {
			vipUuid = createVip("vip-1")
			resp, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, vipUuid)})
			Expect(err).ToNot(HaveOccurred())
			instUuid = resp.GetInstance().GetUUID()
		})

		It("should be readable by owner only", func() {
			resp, err := server.ReadInstance(userCtx("bob"), &svccore.UuidRequest{UUID: instUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetInstance().GetName()).To(Equal("inst-1"))

			_, err = server.ReadInstance(userCtx("alice"), &svccore.UuidRequest{UUID: instUuid})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("should update instance keeping its own vip", func() {
			updated := newInstance("inst-1", zoneUuid, vipUuid)
			updated.Metadata = map[string]string{"env": "prod"}

			_, err := server.UpdateInstance(userCtx("bob"), &svcinstance.UpdateInstanceRequest{UUID: instUuid, Instance: updated})
			Expect(err).ToNot(HaveOccurred())

			stored, err := dbStoreMan.Instances().Read(context.Background(), instUuid)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetMetadata()).To(HaveKeyWithValue("env", "prod"))
			Expect(inst.lastParams.InstanceUUID).To(Equal(instUuid))
		})
		It("should refuse to change zone", func() {
			otherZone := server.createZone("zone-2")

			_, err := server.UpdateInstance(userCtx("bob"), &svcinstance.UpdateInstanceRequest{UUID: instUuid, Instance: newInstance("inst-1", otherZone, vipUuid)})

			expectCode(err, connect.CodeInvalidArgument)
			Expect(err).To(MatchError(ContainSubstring("cannot update zone")))
		})
		It("should refuse to change resource type", func() {
			updated := newInstance("inst-1", zoneUuid, vipUuid)
			updated.ResourceType = "large"

			_, err := server.UpdateInstance(userCtx("bob"), &svcinstance.UpdateInstanceRequest{UUID: instUuid, Instance: updated})

			expectCode(err, connect.CodeInvalidArgument)
			Expect(err).To(MatchError(ContainSubstring("cannot update resource type")))
		})
		It("should refuse to change vips", func() {
			_, err := server.UpdateInstance(userCtx("bob"), &svcinstance.UpdateInstanceRequest{UUID: instUuid, Instance: newInstance("inst-1", zoneUuid)})

			expectCode(err, connect.CodeInvalidArgument)
			Expect(err).To(MatchError(ContainSubstring("use add and delete vips")))
		})
		It("should add and delete a vip", func() {
			otherVip := createVip("vip-2")
			Expect(server.perm.AddPermission(context.Background(), helpers.TypeUrl[*vip.VIP](), otherVip, permission.Role_READER, permission.MemberType_USER, "bob")).To(Succeed())

			_, err := server.AddVipToInstance(userCtx("bob"), &svcinstance.AddVipToInstanceRequest{UUID: instUuid, VipUuid: otherVip})
			Expect(err).ToNot(HaveOccurred())
			_, err = server.AddVipToInstance(userCtx("bob"), &svcinstance.AddVipToInstanceRequest{UUID: instUuid, VipUuid: otherVip})
			Expect(err).ToNot(HaveOccurred(), "adding a vip again does nothing")
			stored, err := dbStoreMan.Instances().Read(context.Background(), instUuid)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetVipsUuid()).To(Equal([]string{vipUuid, otherVip}))

			_, err = server.DeleteVipFromInstance(userCtx("bob"), &svcinstance.DeleteVipFromInstanceRequest{UUID: instUuid, VipUuid: vipUuid})
			Expect(err).ToNot(HaveOccurred())
			stored, err = dbStoreMan.Instances().Read(context.Background(), instUuid)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetVipsUuid()).To(Equal([]string{otherVip}))
		})
		It("should refuse to add a vip the user cannot read", func() {
			resp, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-2")})
			Expect(err).ToNot(HaveOccurred())
			otherVip := resp.GetVip().GetUUID()

			_, err = server.AddVipToInstance(userCtx("bob"), &svcinstance.AddVipToInstanceRequest{UUID: instUuid, VipUuid: otherVip})

			expectCode(err, connect.CodePermissionDenied)
		})
		It("should refuse to add a vip already on another instance of the zone", func() {
			resp, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-2", zoneUuid)})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.AddVipToInstance(userCtx(adminUser), &svcinstance.AddVipToInstanceRequest{UUID: resp.GetInstance().GetUUID(), VipUuid: vipUuid})

			expectCode(err, connect.CodeAlreadyExists)
		})
		It("should refuse vip changes to user who cannot write the instance", func() {
			_, err := server.AddVipToInstance(userCtx("alice"), &svcinstance.AddVipToInstanceRequest{UUID: instUuid, VipUuid: vipUuid})
			expectCode(err, connect.CodePermissionDenied)
			_, err = server.DeleteVipFromInstance(userCtx("alice"), &svcinstance.DeleteVipFromInstanceRequest{UUID: instUuid, VipUuid: vipUuid})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("should give status from instantiater", func() {
			resp, err := server.GetInstanceStatus(userCtx("bob"), &svccore.UuidRequest{UUID: instUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetStatus().GetCurrentState()).To(Equal(instance.InstanceStatus_RUNNING))
		})
		It("should list instances of user", func() {
			resp, err := server.ListInstance(userCtx("bob"), &svcinstance.ListInstanceRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetInstances()).To(HaveLen(1))
		})
		It("should delete instance", func() {
			_, err := server.DeleteInstance(userCtx("bob"), &svccore.UuidRequest{UUID: instUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(dbStoreMan.Instances().Exists(context.Background(), instUuid)).To(BeFalse())
		})
		It("should delete the nodes of the deleted instance only", func() {
			node := &svclookup.InstanceNode{Node: &corev1.Node{}, AgentUuid: "agent-a", Name: "node-a"}
			Expect(dbStoreMan.Nodes().ClaimNode(context.Background(), instUuid, node, "10.0.0.1", 90*time.Second)).To(Succeed())
			Expect(dbStoreMan.Nodes().ClaimNode(context.Background(), "other-instance", node, "10.0.0.1", 90*time.Second)).To(Succeed())

			_, err := server.DeleteInstance(userCtx("bob"), &svccore.UuidRequest{UUID: instUuid})

			Expect(err).ToNot(HaveOccurred())
			Expect(dbStoreMan.Nodes().ListNodes(context.Background(), instUuid)).To(BeEmpty())
			Expect(dbStoreMan.Nodes().ListNodes(context.Background(), "other-instance")).To(HaveLen(1))
			Expect(dbStoreMan.Nodes().DeleteNodes(context.Background(), "other-instance")).To(Succeed())
		})
		It("should delete instance unknown by instantiater", func() {
			inst.deleteErr = fmt.Errorf("service instance %s not found", instUuid)

			_, err := server.DeleteInstance(userCtx("bob"), &svccore.UuidRequest{UUID: instUuid})

			Expect(err).ToNot(HaveOccurred())
			Expect(dbStoreMan.Instances().Exists(context.Background(), instUuid)).To(BeFalse())
		})
		It("should keep instance when instantiater fails to delete it", func() {
			inst.deleteErr = fmt.Errorf("instantiater down")

			_, err := server.DeleteInstance(userCtx("bob"), &svccore.UuidRequest{UUID: instUuid})

			Expect(err).To(MatchError("instantiater down"))
			Expect(dbStoreMan.Instances().Exists(context.Background(), instUuid)).To(BeTrue())
		})

		Context("ListAgent", func() {
			It("should list agents of instance", func() {
				inst.agents = []*agent.Agent{{UUID: "agent-1", Host: "host-1:443"}}

				resp, err := server.ListAgent(userCtx("bob"), &svclookup.ListAgentRequest{InstanceUuid: instUuid})

				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetAgents()).To(HaveLen(1))
				Expect(resp.GetAgents()[0].GetUUID()).To(Equal("agent-1"))
			})
			It("should answer an empty list when instance has no agent", func() {
				resp, err := server.ListAgent(userCtx("bob"), &svclookup.ListAgentRequest{InstanceUuid: instUuid})

				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetAgents()).To(BeEmpty())
			})
			It("should list agents sorted by name", func() {
				inst.agents = []*agent.Agent{{UUID: "agent-2", Name: "lb-b"}, {UUID: "agent-1", Name: "lb-a"}}

				resp, err := server.ListAgent(userCtx("bob"), &svclookup.ListAgentRequest{InstanceUuid: instUuid})

				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetAgents()).To(HaveLen(2))
				Expect(resp.GetAgents()[0].GetName()).To(Equal("lb-a"))
				Expect(resp.GetAgents()[1].GetName()).To(Equal("lb-b"))
				Expect(inst.agents[0].GetName()).To(Equal("lb-b"), "the instantiater's list is left as is")
			})
			It("should be refused to user who cannot read the instance", func() {
				_, err := server.ListAgent(userCtx("alice"), &svclookup.ListAgentRequest{InstanceUuid: instUuid})
				expectCode(err, connect.CodePermissionDenied)
			})
		})

		Context("ListAgentStatus", func() {
			It("should report one error status per agent that cannot be dialed", func() {
				inst.agents = []*agent.Agent{
					{UUID: "agent-ok", Host: "127.0.0.1:1", Features: &agent.AgentFeatures{ShowStatus: true}},
					{UUID: "agent-bad", Host: "[::1", Features: &agent.AgentFeatures{ShowStatus: true}},
				}
				resp, err := server.ListAgentStatus(userCtx("bob"), &svclookup.ListAgentStatusRequest{InstanceUuid: instUuid})
				Expect(err).ToNot(HaveOccurred())
				uuids := make([]string, 0, len(resp.GetAgentStatus()))
				for _, st := range resp.GetAgentStatus() {
					uuids = append(uuids, st.GetAgentUuid())
				}
				Expect(uuids).To(ConsistOf("agent-ok", "agent-bad"))
			})
			It("should be refused to user who cannot read the instance", func() {
				inst.agents = []*agent.Agent{{UUID: "agent-1", Host: "127.0.0.1:1", Features: &agent.AgentFeatures{ShowStatus: true}}}
				_, err := server.ListAgentStatus(userCtx("alice"), &svclookup.ListAgentStatusRequest{InstanceUuid: instUuid})
				expectCode(err, connect.CodePermissionDenied)
			})
		})
	})

	Context("with the ipam", func() {
		BeforeEach(func() {
			server = newTestServerWithIpam(inst, dbIpam)
		})

		It("should acquire the ips of the vips in the zone and release them with the instance", func() {
			vipUuid := createVip("vip-1")

			resp, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, vipUuid)})
			Expect(err).ToNot(HaveOccurred())
			Expect(allocatedIps(zoneUuid)).To(Equal(map[string]string{"10.0.0.1": vipUuid}))

			_, err = server.DeleteInstance(userCtx("bob"), &svccore.UuidRequest{UUID: resp.GetInstance().GetUUID()})
			Expect(err).ToNot(HaveOccurred())
			Expect(allocatedIps(zoneUuid)).To(BeEmpty())
		})
		It("should release the ips of the vips even when the request is cancelled once the instance is deleted", func() {
			vipUuid := createVip("vip-1")
			resp, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, vipUuid)})
			Expect(err).ToNot(HaveOccurred())
			ctx, cancel := context.WithCancel(userCtx("bob"))
			defer cancel()
			inst.onDelete = cancel

			_, err = server.DeleteInstance(ctx, &svccore.UuidRequest{UUID: resp.GetInstance().GetUUID()})

			Expect(err).ToNot(HaveOccurred())
			Expect(allocatedIps(zoneUuid)).To(BeEmpty())
		})
		It("should acquire the same ip in each zone of a vip", func() {
			vipUuid := createVip("vip-1")
			otherZone := server.createZone("zone-2")

			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, vipUuid)})
			Expect(err).ToNot(HaveOccurred())
			_, err = server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-2", otherZone, vipUuid)})
			Expect(err).ToNot(HaveOccurred())

			Expect(allocatedIps(zoneUuid)).To(HaveKey("10.0.0.1"))
			Expect(allocatedIps(otherZone)).To(HaveKey("10.0.0.1"))
		})
		It("should refuse a vip outside of the zone and not create the instance", func() {
			outside := newVip("vip-outside")
			outside.Ip = "192.168.1.1"
			vipResp, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: outside})
			Expect(err).ToNot(HaveOccurred())
			Expect(server.perm.AddPermission(context.Background(), helpers.TypeUrl[*vip.VIP](), vipResp.GetVip().GetUUID(), permission.Role_READER, permission.MemberType_USER, "bob")).To(Succeed())
			inside := createVip("vip-inside")

			_, err = server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, inside, vipResp.GetVip().GetUUID())})

			expectCode(err, connect.CodeFailedPrecondition)
			Expect(dbStoreMan.Instances().List(context.Background(), nil)).To(BeEmpty())
			Expect(allocatedIps(zoneUuid)).To(BeEmpty(), "the ip of the first vip is released")
		})
		It("should release the ips when the instantiater fails", func() {
			vipUuid := createVip("vip-1")
			inst.status = &instance.InstanceStatus{CurrentState: instance.InstanceStatus_ERRORED, ErrorMessage: "no capacity"}

			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, vipUuid)})

			Expect(err).To(HaveOccurred())
			Expect(allocatedIps(zoneUuid)).To(BeEmpty())
		})
		It("should acquire the ip of an added vip and release the ip of a deleted vip", func() {
			resp, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})
			Expect(err).ToNot(HaveOccurred())
			instUuid := resp.GetInstance().GetUUID()
			vipUuid := createVip("vip-1")

			_, err = server.AddVipToInstance(userCtx(adminUser), &svcinstance.AddVipToInstanceRequest{UUID: instUuid, VipUuid: vipUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(allocatedIps(zoneUuid)).To(Equal(map[string]string{"10.0.0.1": vipUuid}))

			_, err = server.DeleteVipFromInstance(userCtx(adminUser), &svcinstance.DeleteVipFromInstanceRequest{UUID: instUuid, VipUuid: vipUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(allocatedIps(zoneUuid)).To(BeEmpty())
		})
		It("should refuse to add a vip whose ip is taken in the zone by another vip", func() {
			resp, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, createVip("vip-1"))})
			Expect(err).ToNot(HaveOccurred())
			sameIp := createVip("vip-2")

			_, err = server.AddVipToInstance(userCtx(adminUser), &svcinstance.AddVipToInstanceRequest{UUID: resp.GetInstance().GetUUID(), VipUuid: sameIp})

			expectCode(err, connect.CodeFailedPrecondition)
			Expect(err).To(MatchError(ContainSubstring("already allocated")))
		})
	})
})
