package procmemory_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pbnjay/memory"

	"github.com/seagalia/flock/agent/starling/procmemory"
)

var _ = Describe("Available", func() {
	It("never goes over the memory of the host", func() {
		available := procmemory.Available()
		Expect(available).To(BeNumerically(">", 0))
		Expect(available).To(BeNumerically("<=", memory.TotalMemory()))
	})
})
