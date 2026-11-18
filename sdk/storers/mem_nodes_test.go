package storers_test

import (
	"context"

	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	flockerrors "github.com/seagalia/flock/sdk/errors"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	. "github.com/seagalia/flock/sdk/storers"
)

var _ = Describe("Memory nodes", func() {
	const staleAfter = 90 * time.Second
	var nodes NodeStore
	BeforeEach(func() {
		nodes = NewMemoryManager().Nodes()
	})
	newNode := func(agentUuid, name string) *svclookup.InstanceNode {
		return &svclookup.InstanceNode{AgentUuid: agentUuid, Name: name}
	}

	It("should refuse a claim from another address while the node is fresh", func() {
		Expect(nodes.ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.1", staleAfter)).To(Succeed())

		err := nodes.ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.2", staleAfter)

		var held *NodeHeldError
		Expect(errors.As(err, &held)).To(BeTrue())
		Expect(held.Addr).To(Equal("10.0.0.1"))
	})
	It("should accept a claim from the same address or over a stale node", func() {
		Expect(nodes.ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.1", staleAfter)).To(Succeed())
		Expect(nodes.ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.1", staleAfter)).To(Succeed())
		time.Sleep(5 * time.Millisecond)

		Expect(nodes.ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.2", time.Millisecond)).To(Succeed())
	})
	It("should list the nodes by name and find the instance of an agent", func() {
		Expect(nodes.ClaimNode(context.Background(), "inst-1", newNode("agent-b", "node-b"), "10.0.0.1", staleAfter)).To(Succeed())
		Expect(nodes.ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.2", staleAfter)).To(Succeed())

		list, err := nodes.ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(list).To(HaveLen(2))
		Expect(list[0].GetName()).To(Equal("node-a"))
		Expect(list[0].GetLastSeen().AsTime()).To(BeTemporally("~", time.Now(), time.Second))
		Expect(nodes.FindNodeInstance(context.Background(), "agent-b")).To(Equal("inst-1"))
		_, err = nodes.FindNodeInstance(context.Background(), "unknown")
		Expect(flockerrors.IsErrNotFound(err)).To(BeTrue())
	})
	It("should touch only a node claimed from the same address", func() {
		Expect(nodes.ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.1", staleAfter)).To(Succeed())
		before, err := nodes.ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		time.Sleep(5 * time.Millisecond)

		Expect(nodes.TouchNode(context.Background(), "inst-1", "agent-a", "10.0.0.2")).To(Succeed())
		untouched, err := nodes.ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(untouched[0].GetLastSeen().AsTime()).To(Equal(before[0].GetLastSeen().AsTime()))

		Expect(nodes.TouchNode(context.Background(), "inst-1", "agent-a", "10.0.0.1")).To(Succeed())
		touched, err := nodes.ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(touched[0].GetLastSeen().AsTime()).To(BeTemporally(">", before[0].GetLastSeen().AsTime()))
	})
})
