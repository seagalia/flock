package flock_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	starling_announce "github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	starling_backendopts "github.com/seagalia/flock/sdk/flock/extensions/backend/starling_options/v1"
	starling_limit "github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
)

// these names end up in google.protobuf.Any type urls stored by controllers, they must never change
var _ = Describe("Type urls", func() {
	It("keeps the stored type urls of the starling agent extensions", func() {
		Expect(string(proto.MessageName(&starling_announce.StarlingAnnounce{}))).To(Equal("flock.extensions.announces.starling_announce.v1.StarlingAnnounce"))
		Expect(string(proto.MessageName(&starling_vsopts.TcpUdpOptions{}))).To(Equal("flock.extensions.vs.starling_options.v1.TcpUdpOptions"))
		Expect(string(proto.MessageName(&starling_backendopts.BackendOptions{}))).To(Equal("flock.extensions.backend.starling_options.v1.BackendOptions"))
		Expect(string(proto.MessageName(&starling_limit.StarlingRateLimit{}))).To(Equal("flock.extensions.limit.starling_limit.v1.StarlingRateLimit"))
	})
})
