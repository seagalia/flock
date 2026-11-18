package dbstore_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	flockerrors "github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/storers"
)

var _ = Describe("Nodes", func() {
	const staleAfter = 90 * time.Second

	newNode := func(agentUuid, name string, typeUrls ...string) *svclookup.InstanceNode {
		node := &core.Node{ClientType: "starling"}
		for _, typeUrl := range typeUrls {
			node.Extensions = append(node.Extensions, &core.Extension{TypeUrl: typeUrl, Category: "category"})
		}
		return &svclookup.InstanceNode{Node: node, AgentUuid: agentUuid, Name: name}
	}

	It("should list the node claimed for an instance with its agent and extensions", func() {
		node := newNode("agent-a", "node-a", "type-1", "type-2")
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", node, "10.0.0.1", staleAfter)).To(Succeed())

		nodes, err := storeMan.Nodes().ListNodes(context.Background(), "inst-1")

		Expect(err).ToNot(HaveOccurred())
		Expect(nodes).To(HaveLen(1))
		Expect(nodes[0].GetAgentUuid()).To(Equal("agent-a"))
		Expect(nodes[0].GetName()).To(Equal("node-a"))
		Expect(nodes[0].GetNode().GetExtensions()).To(HaveLen(2))
		Expect(nodes[0].GetNode().GetExtensions()[1].GetTypeUrl()).To(Equal("type-2"))
		Expect(nodes[0].GetLastSeen().AsTime()).To(BeTemporally("~", time.Now(), time.Second))
	})
	It("should not claim a node on a cancelled ctx", func() {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()

		err := storeMan.Nodes().ClaimNode(cancelled, "inst-cancel", newNode("agent-c", "node-c"), "10.0.0.1", staleAfter)

		Expect(err).To(MatchError(context.Canceled))
		Expect(storeMan.Nodes().ListNodes(context.Background(), "inst-cancel")).To(BeEmpty())
	})
	It("should order the nodes by name", func() {
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-1", "node-b"), "10.0.0.1", staleAfter)).To(Succeed())
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-2", "node-a"), "10.0.0.2", staleAfter)).To(Succeed())

		nodes, err := storeMan.Nodes().ListNodes(context.Background(), "inst-1")

		Expect(err).ToNot(HaveOccurred())
		Expect(nodes).To(HaveLen(2))
		Expect(nodes[0].GetName()).To(Equal("node-a"))
		Expect(nodes[1].GetName()).To(Equal("node-b"))
	})
	It("should refuse a claim from another address while the row is fresh", func() {
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.1", staleAfter)).To(Succeed())

		err := storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.2", staleAfter)

		var held *storers.NodeHeldError
		Expect(errors.As(err, &held)).To(BeTrue())
		Expect(held.Addr).To(Equal("10.0.0.1"))
	})
	It("should accept a claim from another address once the row is stale", func() {
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a", "type-1"), "10.0.0.1", staleAfter)).
			To(Succeed())
		time.Sleep(5 * time.Millisecond)

		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a", "type-2"), "10.0.0.2", time.Millisecond)).
			To(Succeed())

		nodes, err := storeMan.Nodes().ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(nodes).To(HaveLen(1))
		Expect(nodes[0].GetNode().GetExtensions()[0].GetTypeUrl()).To(Equal("type-2"))
	})
	It("should accept a claim from the same address over its own fresh row", func() {
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a", "type-1"), "10.0.0.1", staleAfter)).
			To(Succeed())

		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a", "type-3"), "10.0.0.1", staleAfter)).
			To(Succeed())

		nodes, err := storeMan.Nodes().ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(nodes).To(HaveLen(1))
		Expect(nodes[0].GetNode().GetExtensions()[0].GetTypeUrl()).To(Equal("type-3"))
	})
	It("should accept concurrent claims from the same address", func() {
		const claims = 8
		errs := make([]error, claims)
		var claiming sync.WaitGroup
		for i := range claims {
			claiming.Go(func() {
				errs[i] = storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.1", staleAfter)
			})
		}
		claiming.Wait()

		for _, err := range errs {
			Expect(err).ToNot(HaveOccurred())
		}
		Expect(storeMan.Nodes().ListNodes(context.Background(), "inst-1")).To(HaveLen(1))
	})
	It("should give a node to one of the addresses claiming it at once", func() {
		const claims = 8
		errs := make([]error, claims)
		var claiming sync.WaitGroup
		for i := range claims {
			addr := fmt.Sprintf("10.0.0.%d", i+1)
			claiming.Go(func() {
				errs[i] = storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), addr, staleAfter)
			})
		}
		claiming.Wait()

		var winners []string
		holders := map[string]bool{}
		for i, err := range errs {
			if err == nil {
				winners = append(winners, fmt.Sprintf("10.0.0.%d", i+1))
				continue
			}
			var held *storers.NodeHeldError
			Expect(errors.As(err, &held)).To(BeTrue())
			holders[held.Addr] = true
		}
		Expect(winners).To(HaveLen(1))
		Expect(holders).To(Equal(map[string]bool{winners[0]: true}))
	})
	It("should move the last seen time forward on touch", func() {
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.1", staleAfter)).To(Succeed())
		before, err := storeMan.Nodes().ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		time.Sleep(5 * time.Millisecond)

		Expect(storeMan.Nodes().TouchNode(context.Background(), "inst-1", "agent-a", "10.0.0.1")).To(Succeed())

		after, err := storeMan.Nodes().ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(after[0].GetLastSeen().AsTime()).To(BeTemporally(">", before[0].GetLastSeen().AsTime()))
	})
	It("should leave the last seen time of a node claimed from another address on touch", func() {
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.2", staleAfter)).To(Succeed())
		before, err := storeMan.Nodes().ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		time.Sleep(5 * time.Millisecond)

		Expect(storeMan.Nodes().TouchNode(context.Background(), "inst-1", "agent-a", "10.0.0.1")).To(Succeed())

		after, err := storeMan.Nodes().ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(after[0].GetLastSeen().AsTime()).To(Equal(before[0].GetLastSeen().AsTime()))
	})
	It("should find the instance an agent connected for", func() {
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.1", staleAfter)).To(Succeed())

		Expect(storeMan.Nodes().FindNodeInstance(context.Background(), "agent-a")).To(Equal("inst-1"))
		_, err := storeMan.Nodes().FindNodeInstance(context.Background(), "unknown")
		Expect(flockerrors.IsErrNotFound(err)).To(BeTrue())
	})
	It("should delete every node of an instance and keep the others", func() {
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-a", "node-a"), "10.0.0.1", staleAfter)).To(Succeed())
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-1", newNode("agent-b", "node-b"), "10.0.0.2", staleAfter)).To(Succeed())
		Expect(storeMan.Nodes().ClaimNode(context.Background(), "inst-2", newNode("agent-c", "node-a"), "10.0.0.3", staleAfter)).To(Succeed())

		Expect(storeMan.Nodes().DeleteNodes(context.Background(), "inst-1")).To(Succeed())

		nodes, err := storeMan.Nodes().ListNodes(context.Background(), "inst-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(nodes).To(BeEmpty())
		other, err := storeMan.Nodes().ListNodes(context.Background(), "inst-2")
		Expect(err).ToNot(HaveOccurred())
		Expect(other).To(HaveLen(1))
	})
})
