package helpers_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	. "github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("Validate", func() {
	agentNamed := func(name string) *agent.Agent {
		return &agent.Agent{
			UUID:     "agent-1",
			Name:     name,
			Host:     "10.0.0.1:8443",
			Type:     "starling",
			Features: &agent.AgentFeatures{},
		}
	}

	DescribeTable("the name of an agent",
		func(name string, valid bool) {
			err := Validate(agentNamed(name))
			if valid {
				Expect(err).ToNot(HaveOccurred())
				return
			}
			Expect(err).To(HaveOccurred())
		},
		Entry("lowercase with a dash", "edge-1", true),
		Entry("a single character", "a", true),
		Entry("63 characters", strings.Repeat("a", 63), true),
		Entry("uppercase and underscore", "Edge_1", false),
		Entry("64 characters", strings.Repeat("a", 64), false),
		Entry("starting with a dash", "-edge", false),
		Entry("ending with a dash", "edge-", false),
		Entry("empty", "", false),
	)
})

var _ = Describe("Validate ReadAgentRequest", func() {
	DescribeTable("the agent uuid",
		func(value string, valid bool) {
			err := Validate(&svclookup.ReadAgentRequest{AgentUuid: value})
			if valid {
				Expect(err).ToNot(HaveOccurred())
				return
			}
			Expect(err).To(HaveOccurred())
		},
		Entry("a uuid", "6ba7b810-9dad-11d1-80b4-00c04fd430c8", true),
		Entry("empty", "", false),
		Entry("a declared id that is not a uuid", "agent-1", true),
	)
})
