package svcdiscovery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	. "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Resource", func() {
	It("should be made from a message and converted back", func() {
		aBackend := &backend.Backend{UUID: "backend-1", Name: "my-backend"}
		resource, err := NewResourceFromMessage(aBackend)
		Expect(err).ToNot(HaveOccurred())
		Expect(resource.GetUuid()).To(Equal("backend-1"))
		expectedSig, err := helpers.MessageSignature(aBackend)
		Expect(err).ToNot(HaveOccurred())
		Expect(resource.GetSignature()).To(Equal(expectedSig))

		converted := &backend.Backend{}
		Expect(resource.ToMessage(converted)).To(Succeed())
		Expect(converted.GetName()).To(Equal("my-backend"))
	})

	It("should fail to convert to another message type", func() {
		resource, err := NewResourceFromMessage(&backend.Backend{UUID: "backend-1"})
		Expect(err).ToNot(HaveOccurred())
		Expect(resource.ToMessage(wrapperspb.String(""))).ToNot(Succeed())
	})

	It("should leave the message untouched when there is no resource", func() {
		converted := &backend.Backend{Name: "untouched"}
		Expect((&Resource{}).ToMessage(converted)).To(Succeed())
		Expect(converted.GetName()).To(Equal("untouched"))
	})
})

var _ = Describe("DeltaDiscoveryResponse", func() {
	It("should describe a response for logs by uuids, without the resources nor their versions", func() {
		aSecret, err := NewResourceFromMessage(&secret.Secret{UUID: "s1", Data: map[string]string{"k": "very-secret"}})
		Expect(err).ToNot(HaveOccurred())
		resp := &DeltaDiscoveryResponse{ResponseNonce: "nonce-1", Resources: []*Resource{aSecret}, RemovedResources: []string{"s2"}}

		Expect(resp.LogSummary()).To(Equal("nonce=nonce-1 resources=[s1] removed=[s2]"))
	})
})
