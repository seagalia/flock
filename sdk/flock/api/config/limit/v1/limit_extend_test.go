package limit_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	. "github.com/seagalia/flock/sdk/flock/api/config/limit/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	flocktype "github.com/seagalia/flock/sdk/flock/type/v1"
)

var _ = Describe("Limit", func() {
	It("should give its starling agent limit", func() {
		agentLimit, err := (&Config{}).GetStarlingLimit()
		Expect(err).ToNot(HaveOccurred())
		Expect(agentLimit).To(BeNil())

		packed, err := anypb.New(&starling_limit.StarlingRateLimit{})
		Expect(err).ToNot(HaveOccurred())
		agentLimit, err = (&Config{Action: &Config_TypedLimit{TypedLimit: packed}}).GetStarlingLimit()
		Expect(err).ToNot(HaveOccurred())
		Expect(agentLimit).ToNot(BeNil())

		wrong, err := anypb.New(wrapperspb.String("other"))
		Expect(err).ToNot(HaveOccurred())
		_, err = (&Config{Action: &Config_TypedLimit{TypedLimit: wrong}}).GetStarlingLimit()
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("should format connection rate as nftables limit",
		func(unit flocktype.TimeUnit, expected string) {
			Expect((&ConnectionRateLimit{Rate: 10, Unit: unit}).RateAsNftables()).To(Equal(expected))
		},
		Entry("without unit", flocktype.TimeUnit_UNKNOWN, "10/second"),
		Entry("per second", flocktype.TimeUnit_SECOND, "10/second"),
		Entry("per minute", flocktype.TimeUnit_MINUTE, "10/minute"),
		Entry("per hour", flocktype.TimeUnit_HOUR, "10/hour"),
		Entry("per day", flocktype.TimeUnit_DAY, "10/day"),
	)
})
