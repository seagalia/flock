package multi

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Nats", func() {
	It("publishes controller events under the controller subject prefix", func() {
		Expect((&Nats{}).transformTopic("vip.created")).To(Equal("controller.vip.created"))
	})
})
