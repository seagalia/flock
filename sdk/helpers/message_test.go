package helpers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	. "github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Message helpers", func() {
	var aBackend *backend.Backend

	BeforeEach(func() {
		aBackend = &backend.Backend{UUID: "backend-1", Name: "my-backend"}
	})

	Context("TypeUrl", func() {
		It("should give the type url of a message type", func() {
			Expect(TypeUrl[*backend.Backend]()).To(Equal("type.googleapis.com/flock.api.config.backend.v1.Backend"))
			Expect(TypeUrlFromMessage(aBackend)).To(Equal(TypeUrl[*backend.Backend]()))
		})
	})

	Context("GetIdentifier", func() {
		It("should give the identifier of an identifiable message", func() {
			Expect(GetIdentifier(aBackend)).To(Equal("backend-1"))
		})

		It("should give the signature of a message which is not identifiable", func() {
			message := wrapperspb.String("value")
			sig, err := MessageSignature(message)
			Expect(err).ToNot(HaveOccurred())
			Expect(GetIdentifier(message)).To(Equal(sig))
		})
	})

	Context("SetIdentifier", func() {
		It("should set the identifier of an identifiable message", func() {
			SetIdentifier(aBackend, "backend-2")
			Expect(aBackend.GetUUID()).To(Equal("backend-2"))
		})

		It("should ignore a message which is not identifiable", func() {
			message := wrapperspb.String("value")
			SetIdentifier(message, "id")
			Expect(message.GetValue()).To(Equal("value"))
		})
	})

	Context("GetName", func() {
		It("should give the name of a named message or nothing", func() {
			Expect(GetName(aBackend)).To(Equal("my-backend"))
			Expect(GetName(wrapperspb.String("value"))).To(BeEmpty())
		})
	})

	Context("GetMetadata", func() {
		It("should give metadata of a message with metadata or an empty map", func() {
			aBackend.AddMetadata("key", "value")
			Expect(GetMetadata(aBackend)).To(Equal(map[string]string{"key": "value"}))
			Expect(GetMetadata(wrapperspb.String("value"))).To(BeEmpty())
		})
	})

	Context("ProvDefault", func() {
		It("should apply defaults of a message having defaults", func() {
			ProvDefault(aBackend)
			Expect(aBackend.GetRefreshRate()).ToNot(BeNil())
		})

		It("should ignore a message without defaults", func() {
			Expect(func() { ProvDefault(wrapperspb.String("value")) }).ToNot(Panic())
		})
	})

	Context("Validate", func() {
		It("should accept nil", func() {
			Expect(Validate(nil)).To(Succeed())
		})

		It("should fail on the protovalidate rules of the message", func() {
			Expect(Validate(&core.IPAddr{Ip: "not an ip"})).ToNot(Succeed())
		})

		It("should fail when a required oneof is not set", func() {
			err := Validate(&vs.VirtualServer{Name: "my-vs", Port: 80})
			Expect(err).To(MatchError(ContainSubstring("route_config")))
		})

		It("should accept a valid message", func() {
			Expect(Validate(&core.IPAddr{Ip: "10.0.0.1", Port: 80})).To(Succeed())
		})
	})

	Context("MessageSignature", func() {
		It("should be the same for equal messages and differ for different ones", func() {
			sig1, err := MessageSignature(aBackend)
			Expect(err).ToNot(HaveOccurred())
			sig2, err := MessageSignature(&backend.Backend{UUID: "backend-1", Name: "my-backend"})
			Expect(err).ToNot(HaveOccurred())
			Expect(sig1).To(Equal(sig2))

			sig3, err := MessageSignature(&backend.Backend{UUID: "backend-1", Name: "other"})
			Expect(err).ToNot(HaveOccurred())
			Expect(sig3).ToNot(Equal(sig1))
		})

		It("should follow changes of a nested message already signed", func() {
			group := &core.IPAddrGroup{IpAddresses: []*core.IPAddr{{Ip: "10.0.0.1"}}}
			aBackend.Endpoint = &backend.Backend_IpAddrGroup{IpAddrGroup: group}
			_, err := MessageSignature(aBackend)
			Expect(err).ToNot(HaveOccurred())

			group.IpAddresses[0].Ip = "200.200.200.200"
			sig, err := MessageSignature(aBackend)
			Expect(err).ToNot(HaveOccurred())
			expected, err := MessageSignature(&backend.Backend{
				UUID: "backend-1", Name: "my-backend",
				Endpoint: &backend.Backend_IpAddrGroup{IpAddrGroup: &core.IPAddrGroup{
					IpAddresses: []*core.IPAddr{{Ip: "200.200.200.200"}},
				}},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(sig).To(Equal(expected))
		})
	})
})
