package agentlist_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("AgentList", func() {
	It("should leave the cluster when its ctx ends, so the others see it gone before any failure detection", func() {
		port1, port2 := freePort(), freePort()
		list1, _ := startAgentList("node-1", port1, port2)
		_, stopList2 := startAgentList("node-2", port2, port1)
		Eventually(func() int { return len(list1.Members()) }, 10*time.Second).Should(Equal(2))

		Expect(stopList2()).To(Succeed())

		// a node gone without leaving stays a member until its suspicion times out, 4s at least with the lan config
		Eventually(func() int { return len(list1.Members()) }, 2*time.Second).Should(Equal(1))
	})
})
