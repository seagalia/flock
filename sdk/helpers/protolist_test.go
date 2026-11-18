package helpers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	. "github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("MarshalListProtoMessage", func() {
	It("should give a json array of messages", func() {
		b, err := MarshalListProtoMessage(protojson.MarshalOptions{}, []*backend.Backend{{UUID: "a"}, {UUID: "b"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(b)).To(MatchJSON(`[{"UUID":"a"},{"UUID":"b"}]`))
	})

	It("should give an empty json array for no messages", func() {
		b, err := MarshalListProtoMessage(protojson.MarshalOptions{}, []*backend.Backend{})
		Expect(err).ToNot(HaveOccurred())
		Expect(string(b)).To(Equal("[]"))
	})
})
