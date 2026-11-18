package protoset

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GetAllInfo", func() {
	// explain and the field docs of the webui show proto comments, and only the protoset carries them
	It("keeps proto comments", func() {
		methods, _, err := GetAllInfo()
		Expect(err).ToNot(HaveOccurred())

		var comment string
		for _, method := range methods {
			if method.GetFullyQualifiedName() == "flock.services.vs.v1.VirtualServer.CreateVS" {
				comment = method.GetSourceInfo().GetLeadingComments()
			}
		}
		Expect(comment).To(ContainSubstring("creates a new virtual server"))
	})
})
