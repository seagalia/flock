package controller

import (
	"context"
	"errors"

	"google.golang.org/protobuf/types/known/anypb"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	agentapi "github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	backendv1 "github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	instancev1 "github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

type fakeInstances struct {
	uuids []string
}

func (f *fakeInstances) ListInstance(
	_ context.Context, req *svcinstance.ListInstanceRequest,
) (*svcinstance.ListInstanceResponse, error) {
	resp := &svcinstance.ListInstanceResponse{}
	for _, uuid := range f.uuids[min(int(req.GetOffset()), len(f.uuids)):] {
		resp.Instances = append(resp.Instances, &instancev1.Instance{UUID: uuid})
	}
	return resp, nil
}

type agentKey struct {
	instance, agent string
}

type fakeLookup struct {
	agents   map[string][]*agentapi.Agent
	statuses map[agentKey][]*svclookup.StatusResponse
	failing  bool
}

func (f *fakeLookup) ListAgent(
	_ context.Context, req *svclookup.ListAgentRequest,
) (*svclookup.ListAgentResponse, error) {
	return &svclookup.ListAgentResponse{Agents: f.agents[req.GetInstanceUuid()]}, nil
}

func (f *fakeLookup) ListStatus(
	_ context.Context, req *svclookup.ListStatusRequest,
) (*svclookup.ListStatusResponse, error) {
	if f.failing {
		return nil, errors.New("agent unreachable")
	}
	return &svclookup.ListStatusResponse{Status: f.statuses[agentKey{req.GetInstanceUuid(), req.GetAgentUuid()}]}, nil
}

func backendStatus(agentUuid, backendUuid string, state svclookup.StatusResponse_State, errText string) *svclookup.StatusResponse {
	resource, err := anypb.New(&backendv1.Backend{UUID: backendUuid})
	Expect(err).ToNot(HaveOccurred())
	return &svclookup.StatusResponse{AgentUuid: agentUuid, PreviousResource: resource, State: state, Error: errText}
}

var _ = Describe("AgentStatusPoller", func() {
	var (
		ctx     context.Context
		lookup  *fakeLookup
		changed chan string
		poller  *AgentStatusPoller
	)

	received := func() []string {
		var uuids []string
		for len(changed) > 0 {
			uuids = append(uuids, <-changed)
		}
		return uuids
	}

	BeforeEach(func() {
		ctx = context.Background()
		lookup = &fakeLookup{
			agents: map[string][]*agentapi.Agent{
				"inst-1": {
					{UUID: "lb-1"},
					{UUID: "lb-2"},
					{UUID: "prov-1", Mode: agentapi.Agent_PROVISIONER},
				},
			},
			statuses: map[agentKey][]*svclookup.StatusResponse{
				{"inst-1", "lb-1"}: {
					backendStatus("lb-1", "b-1", svclookup.StatusResponse_OK, ""),
					backendStatus("lb-1", "b-2", svclookup.StatusResponse_ADDING, ""),
				},
				{"inst-1", "lb-2"}: {backendStatus("lb-2", "b-1", svclookup.StatusResponse_ERRORED, "dns lookup failed")},
			},
		}
		changed = make(chan string, 10)
		poller = NewAgentStatusPoller(&fakeInstances{uuids: []string{"inst-1"}}, lookup,
			helpers.TypeUrl[*backendv1.Backend](), 0, changed)
	})

	It("keeps the worst state among the agents, with the error of each failing agent", func() {
		Expect(poller.poll(ctx)).To(Succeed())

		state, ok := poller.State("b-1")
		Expect(ok).To(BeTrue())
		Expect(state.State).To(Equal(svclookup.StatusResponse_ERRORED))
		Expect(state.Errors).To(Equal([]string{"lb-2: dns lookup failed"}))
		state, _ = poller.State("b-2")
		Expect(state.State).To(Equal(svclookup.StatusResponse_ADDING))
		Expect(received()).To(ConsistOf("b-1", "b-2"))
	})

	It("sends a resource again only when its state changes", func() {
		Expect(poller.poll(ctx)).To(Succeed())
		received()

		Expect(poller.poll(ctx)).To(Succeed())
		Expect(received()).To(BeEmpty())

		lookup.statuses[agentKey{"inst-1", "lb-2"}] = []*svclookup.StatusResponse{
			backendStatus("lb-2", "b-1", svclookup.StatusResponse_OK, ""),
		}
		Expect(poller.poll(ctx)).To(Succeed())
		Expect(received()).To(Equal([]string{"b-1"}))
		state, _ := poller.State("b-1")
		Expect(state.State).To(Equal(svclookup.StatusResponse_OK))
	})

	It("sends a resource no agent holds any more", func() {
		Expect(poller.poll(ctx)).To(Succeed())
		received()

		lookup.statuses = map[agentKey][]*svclookup.StatusResponse{}
		Expect(poller.poll(ctx)).To(Succeed())

		Expect(received()).To(ConsistOf("b-1", "b-2"))
		_, ok := poller.State("b-1")
		Expect(ok).To(BeFalse())
	})

	It("keeps the last states when an agent can't be reached", func() {
		Expect(poller.poll(ctx)).To(Succeed())
		received()

		lookup.failing = true
		Expect(poller.poll(ctx)).To(MatchError(ContainSubstring("agent unreachable")))

		Expect(received()).To(BeEmpty())
		state, _ := poller.State("b-1")
		Expect(state.State).To(Equal(svclookup.StatusResponse_ERRORED))
	})

	It("ignores the agents that know nothing of the resource", func() {
		lookup.statuses[agentKey{"inst-1", "lb-2"}] = []*svclookup.StatusResponse{
			backendStatus("lb-2", "b-1", svclookup.StatusResponse_UNKNOWN, ""),
		}

		Expect(poller.poll(ctx)).To(Succeed())

		state, _ := poller.State("b-1")
		Expect(state.State).To(Equal(svclookup.StatusResponse_OK))
	})
})
