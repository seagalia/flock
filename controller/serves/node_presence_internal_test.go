package serves

import (
	"context"

	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/storers"
)

// touchCounter counts the touches of each node from the address its specs open it with
type touchCounter struct {
	storers.NodeStore
	mutex   sync.Mutex
	touches map[nodeKey]int
}

func (c *touchCounter) TouchNode(_ context.Context, instanceUuid, agentUuid, addr string) error {
	if addr != "10.0.0.1" {
		return nil
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.touches[nodeKey{instanceUuid, agentUuid}]++
	return nil
}

func (c *touchCounter) count(key nodeKey) int {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.touches[key]
}

var _ = Describe("nodePresence", func() {
	const refreshEvery = 20 * time.Millisecond
	var (
		counter  *touchCounter
		presence *nodePresence
		key      nodeKey
	)
	BeforeEach(func() {
		counter = &touchCounter{touches: map[nodeKey]int{}}
		presence = newNodePresence(counter, refreshEvery, zerolog.Nop())
		key = nodeKey{instanceUuid: "instance-1", agentUuid: "agent-1"}
	})

	It("should touch the node again and again while one of its streams is open", func() {
		presence.open(context.Background(), key, "10.0.0.1")
		DeferCleanup(presence.close, key)

		Eventually(func() int { return counter.count(key) }).Should(BeNumerically(">=", 3))
	})
	It("should keep refreshing until the last stream of the node closes", func() {
		presence.open(context.Background(), key, "10.0.0.1")
		presence.open(context.Background(), key, "10.0.0.1")
		presence.close(context.Background(), key)
		touchesAfterFirstClose := counter.count(key)

		Eventually(func() int { return counter.count(key) }).Should(BeNumerically(">", touchesAfterFirstClose))
		presence.close(context.Background(), key)
	})
	It("should touch the node once more then stop when its last stream closes", func() {
		presence.open(context.Background(), key, "10.0.0.1")
		presence.close(context.Background(), key)

		Expect(counter.count(key)).To(Equal(1))
		Consistently(func() int { return counter.count(key) }, 5*refreshEvery).Should(Equal(1))
	})
})
