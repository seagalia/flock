package agentserv_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/agent/f5/agentserv"
	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/agent/f5/bigip"
	"github.com/seagalia/flock/agent/f5/reconciler"
	"github.com/seagalia/flock/sdk/agentsdk/loghooks"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

const backendUUID = "33333333-3333-3333-3333-333333333333"

type fakeReconciler struct {
	status reconciler.Status
	pools  []as3.PoolRef
}

func (f *fakeReconciler) Status() reconciler.Status { return f.status }

func (f *fakeReconciler) PoolsOfBackend(uuid string) []as3.PoolRef {
	var pools []as3.PoolRef
	for _, p := range f.pools {
		if p.BackendUUID == uuid {
			pools = append(pools, p)
		}
	}
	return pools
}

type fakeDevice struct {
	devices []bigip.DeviceHealth
	members map[string]map[string]bool
}

func (f *fakeDevice) Devices(context.Context) []bigip.DeviceHealth { return f.devices }

func (f *fakeDevice) PoolMemberStates(_ context.Context, _, _, pool string) (map[string]bool, error) {
	return f.members[pool], nil
}

var _ = Describe("Agent server", func() {
	var (
		rec    *fakeReconciler
		dev    *fakeDevice
		server *agentserv.AgentServer
		ctx    = context.Background()
	)

	BeforeEach(func() {
		rec = &fakeReconciler{status: reconciler.Status{FailedTenants: map[string]string{}}}
		dev = &fakeDevice{members: map[string]map[string]bool{}}
		server = agentserv.NewAgentServer("f5-agent", rec, dev, loghooks.NewListenerWriter(), rt.Store.Backends(), rt.PrevStore, stores.NewStateStore(), rt.SecretLinks, agentserv.TlsInfo{})
	})

	Describe("resource status", func() {
		var stateStore *stores.StateStore

		BeforeEach(func() {
			stateStore = stores.NewStateStore()
			server = agentserv.NewAgentServer("f5-agent", rec, dev, loghooks.NewListenerWriter(), rt.Store.Backends(), rt.PrevStore, stateStore, rt.SecretLinks, agentserv.TlsInfo{})
			v := &vs.VirtualServer{UUID: "vs-1", Name: "web"}
			Expect(rt.PrevStore.VirtualServers().Create(context.Background(), v)).To(Succeed())
			stateStore.SetNotes(helpers.TypeUrl[*vs.VirtualServer](), "vs-1", "f5", []*flocktype.Note{{
				Level: flocktype.Note_WARNING, Code: "f5.approximation", Message: "enable_l3_log is not supported by the F5 agent, it is ignored",
			}})
		})

		It("gives the notes of a resource", func() {
			status, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: helpers.TypeUrl[*vs.VirtualServer](), Identifier: "vs-1"})

			Expect(err).ToNot(HaveOccurred())
			Expect(status.GetState()).To(Equal(agentstatus.StatusResponse_OK))
			Expect(status.GetNotes()).To(ConsistOf(HaveField("Message", "enable_l3_log is not supported by the F5 agent, it is ignored")))
		})

		It("puts the secret references back in the errored resource", func() {
			vsType := helpers.TypeUrl[*vs.VirtualServer]()
			// the ads handler writes the prev store with the references, the state store gets the resolved message
			resolved := &vs.VirtualServer{UUID: "vs-1", Name: "web", Metadata: map[string]string{"a": "v"}}
			unresolved := &vs.VirtualServer{UUID: "vs-1", Name: "web", Metadata: map[string]string{"a": "secret://s1/k"}}
			Expect(rt.PrevStore.VirtualServers().Create(context.Background(), unresolved)).To(Succeed())
			rt.SecretLinks.Set(vsType, "vs-1", []secret.FoundRef{{Path: `metadata["a"]`, Ref: secret.Ref{SecretUUID: "s1", Key: "k"}}})
			stateStore.AddError(resolved, errors.New("rejected"))

			status, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: vsType, Identifier: "vs-1"})

			Expect(err).ToNot(HaveOccurred())
			previous := &vs.VirtualServer{}
			Expect(status.GetPreviousResource().UnmarshalTo(previous)).To(Succeed())
			Expect(previous.GetMetadata()).To(Equal(map[string]string{"a": "secret://s1/k"}))
			errored := &vs.VirtualServer{}
			Expect(status.GetErroredResource().UnmarshalTo(errored)).To(Succeed())
			Expect(errored.GetMetadata()).To(Equal(map[string]string{"a": "secret://s1/k"}))
		})

		It("refuses the status of a secret, which holds values", func() {
			Expect(rt.PrevStore.Secrets().Create(context.Background(), &secret.Secret{UUID: "s1", Data: map[string]string{"k": "v"}})).
				To(Succeed())

			status, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{TypeUrl: helpers.TypeUrl[*secret.Secret](), Identifier: "s1"})

			Expect(err).To(HaveOccurred())
			Expect(status).To(BeNil())
		})

		It("lists the notes of every resource", func() {
			list, err := server.ListStatus(ctx, &agentstatus.ListStatusRequest{TypeUrl: helpers.TypeUrl[*vs.VirtualServer]()})

			Expect(err).ToNot(HaveOccurred())
			Expect(list.GetStatuses()).To(HaveLen(1))
			Expect(list.GetStatuses()[0].GetNotes()).To(HaveLen(1))
		})
	})

	It("tells an endpoint down in one of the pools of its backend is unhealthy", func() {
		rec.pools = []as3.PoolRef{
			{BackendUUID: backendUUID, Tenant: "flock", App: "vip_a", Pool: "pool_1"},
			{BackendUUID: backendUUID, Tenant: "flock", App: "vip_b", Pool: "pool_2"},
		}
		dev.members["pool_1"] = map[string]bool{"10.0.0.1:80": true, "10.0.0.2:80": true}
		dev.members["pool_2"] = map[string]bool{"10.0.0.1:80": false, "10.0.0.2:80": true}

		status, err := server.GetStatus(ctx, &agentstatus.GetStatusRequest{
			TypeUrl: helpers.TypeUrl[*backend.BackendEndpointStats](), Identifier: backendUUID,
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(status.GetState()).To(Equal(agentstatus.StatusResponse_ERRORED))
		var stats backend.BackendEndpointStats
		Expect(status.GetPreviousResource().UnmarshalTo(&stats)).To(Succeed())
		Expect(stats.GetHealthy()).To(BeFalse())
		Expect(stats.GetEndpointStats()).To(HaveLen(2))
		first, second := stats.GetEndpointStats()[0], stats.GetEndpointStats()[1]
		Expect(first.GetIpAddr()).To(BeComparableTo(&core.IPAddr{Ip: "10.0.0.1", Port: 80}, protocmp.Transform()))
		Expect(first.GetHealthy()).To(BeFalse())
		Expect(first.GetLastError().GetValue()).To(Equal("down in pool /flock/vip_b/pool_2"))
		Expect(second.GetHealthy()).To(BeTrue())
	})

	Describe("agent status", func() {
		BeforeEach(func() {
			dev.devices = []bigip.DeviceHealth{
				{URL: "https://bigip-1", Reachable: true, Active: true, FailoverStatus: "ACTIVE", AS3Version: "3.56.0"},
				{URL: "https://bigip-2", Reachable: true, FailoverStatus: "STANDBY", AS3Version: "3.56.0"},
				{URL: "https://bigip-3", Err: errors.New("connection refused")},
				{URL: "https://bigip-4", Reachable: true, Active: true, FailoverStatus: "ACTIVE", AS3Version: "3.45.0"},
			}
			rec.status.PostedVIPs = []*vip.VIP{{UUID: "v1", Ip: "10.99.0.1"}}
		})

		It("gives one component per device and one for the reconciler", func() {
			status, err := server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{ShowAnnouncingVips: true})

			Expect(err).ToNot(HaveOccurred())
			bridge := status.GetProxyAgentState()
			Expect(bridge).ToNot(BeNil())
			states := map[string]*agent.State{}
			for _, s := range bridge.GetComponentStates() {
				states[s.GetName()] = s
			}
			Expect(states["bigip https://bigip-1"].GetStatus()).To(Equal(agent.Status_OK))
			Expect(states["bigip https://bigip-2"].GetStatus()).To(Equal(agent.Status_WARNING))
			Expect(states["bigip https://bigip-2"].GetDetails()).To(Equal("STANDBY"))
			Expect(states["bigip https://bigip-3"].GetStatus()).To(Equal(agent.Status_ERRORED))
			Expect(states["bigip https://bigip-3"].GetDetails()).To(ContainSubstring("connection refused"))
			Expect(states["bigip https://bigip-4"].GetStatus()).To(Equal(agent.Status_WARNING))
			Expect(states["bigip https://bigip-4"].GetDetails()).To(ContainSubstring("AS3 3.45.0 is older than 3.50.0"))
			Expect(states["reconciler"].GetStatus()).To(Equal(agent.Status_OK))
			Expect(bridge.GetComponentAnnouncingVips()).To(HaveLen(1))
			Expect(bridge.GetComponentAnnouncingVips()[0].GetName()).To(Equal("bigip https://bigip-1"))
			Expect(bridge.GetComponentAnnouncingVips()[0].GetAnnouncingVips()).To(HaveLen(1))
		})

		It("reports the tenants the device refused", func() {
			rec.status.FailedTenants = map[string]string{"team1": "bigip: 422 declaration failed"}

			status, err := server.GetAgentStatus(ctx, &agentstatus.GetAgentStatusRequest{})

			Expect(err).ToNot(HaveOccurred())
			for _, s := range status.GetProxyAgentState().GetComponentStates() {
				if s.GetName() == "reconciler" {
					Expect(s.GetStatus()).To(Equal(agent.Status_ERRORED))
					Expect(s.GetDetails()).To(Equal("tenant team1: bigip: 422 declaration failed"))
				}
			}
		})
	})

	It("lists the resource types it reports", func() {
		types, err := server.ListResourceTypes(ctx, &emptypb.Empty{})

		Expect(err).ToNot(HaveOccurred())
		Expect(types.GetResourceTypes()).To(ConsistOf(
			helpers.TypeUrl[*vip.VIP](), helpers.TypeUrl[*vs.VirtualServer](), helpers.TypeUrl[*backend.Backend](),
			helpers.TypeUrl[*cert.Certificate](), helpers.TypeUrl[*backend.BackendEndpointStats](),
		))
	})
})
