package config_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"go.yaml.in/yaml/v2"
)

var _ = Describe("Instantiators", func() {
	Context("Instantiator", func() {
		It("should default originating identity to controller", func() {
			inst := &Instantiator{}
			content := `
name: inst
zones: [z1]
resource_types: [{name: small, cpu: 100}]
static: {agents: [{uuid: u, name: n, host: h}]}
`
			err := yaml.Unmarshal([]byte(content), inst)
			Expect(err).ToNot(HaveOccurred())

			Expect(inst.OriginatingIdentity).To(Equal("controller"))
			Expect(inst.ResourceTypes).To(HaveLen(1))
			Expect(inst.ResourceTypes[0].CPU).To(Equal(100))
		})
		DescribeTable("should reject invalid instantiator",
			func(content string, errMsg string) {
				err := yaml.Unmarshal([]byte(content), &Instantiator{})
				Expect(err).To(MatchError(ContainSubstring(errMsg)))
			},
			Entry("no name", `
zones: [z1]
resource_types: [{name: small}]
static: {agents: [{uuid: u, name: n, host: h}]}
`, "name is required"),
			Entry("no zones", `
name: inst
resource_types: [{name: small}]
static: {agents: [{uuid: u, name: n, host: h}]}
`, "at least one zone is required"),
			Entry("empty zones", `
name: inst
zones: []
resource_types: [{name: small}]
static: {agents: [{uuid: u, name: n, host: h}]}
`, "at least one zone is required"),
			Entry("no resource types", `
name: inst
zones: [z1]
static: {agents: [{uuid: u, name: n, host: h}]}
`, "at least one resource_type is required"),
			Entry("no instantiator", `
name: inst
zones: [z1]
resource_types: [{name: small}]
`, "at least one instantiator is required"),
		)
	})

	Context("StaticInstantiator", func() {
		It("should require at least one agent", func() {
			err := yaml.Unmarshal([]byte("agents: []\n"), &StaticInstantiator{})
			Expect(err).To(MatchError(ContainSubstring("at least one agent is required")))
		})
		It("should refuse two agents with one name", func() {
			content := "agents: [{uuid: u1, name: edge-1, host: h1}, {uuid: u2, name: edge-1, host: h2}]\n"
			err := yaml.Unmarshal([]byte(content), &StaticInstantiator{})
			Expect(err).To(MatchError(ContainSubstring("edge-1")))
		})
	})

	Context("AgentInstantiator", func() {
		It("should set defaults", func() {
			ag := &AgentInstantiator{}
			err := yaml.Unmarshal([]byte("uuid: u\nname: n\nhost: h:443\n"), ag)
			Expect(err).ToNot(HaveOccurred())

			Expect(ag.Type).To(Equal("starling"))
			Expect(ag.Extensions).To(BeEmpty())
			Expect(ag.Extensions).ToNot(BeNil())
		})
		DescribeTable("should reject agent missing field",
			func(content string, errMsg string) {
				err := yaml.Unmarshal([]byte(content), &AgentInstantiator{})
				Expect(err).To(MatchError(ContainSubstring(errMsg)))
			},
			Entry("no uuid", "name: n\nhost: h\n", "uuid is required"),
			Entry("no name", "uuid: u\nhost: h\n", "name is required"),
			Entry("no host", "uuid: u\nname: n\n", "host is required"),
		)
		DescribeTable("should reject a name breaking the rule",
			func(name string) {
				err := yaml.Unmarshal([]byte("uuid: u\nname: "+name+"\nhost: h\n"), &AgentInstantiator{})
				Expect(err).To(MatchError(And(ContainSubstring(name), ContainSubstring("^[a-z0-9]"))))
			},
			Entry("upper case and underscore", "Edge_1"),
			Entry("leading hyphen", "-edge"),
			Entry("trailing hyphen", "edge-"),
			Entry("too long", strings.Repeat("a", 64)),
		)
		It("should read the provisioner mode", func() {
			ag := &AgentInstantiator{}
			Expect(yaml.Unmarshal([]byte("uuid: u\nname: n\nhost: h\nmode: provisioner\n"), ag)).To(Succeed())

			Expect(ag.ToAgent().GetMode()).To(Equal(agent.Agent_PROVISIONER))
		})
		It("should convert to agent", func() {
			ag := &AgentInstantiator{}
			content := `
uuid: u
name: n
host: h:443
type: custom
extensions: [ext]
mode: bridge
features:
  show_logs: true
  show_metrics: true
  show_status: true
  show_l3_log: true
`
			err := yaml.Unmarshal([]byte(content), ag)
			Expect(err).ToNot(HaveOccurred())

			converted := ag.ToAgent()

			Expect(converted.GetUUID()).To(Equal("u"))
			Expect(converted.GetName()).To(Equal("n"))
			Expect(converted.GetHost()).To(Equal("h:443"))
			Expect(converted.GetType()).To(Equal("custom"))
			Expect(converted.GetExtensions()).To(Equal([]string{"ext"}))
			Expect(converted.GetMode()).To(Equal(agent.Agent_BRIDGE))
			Expect(converted.GetFeatures().GetShowLogs()).To(BeTrue())
			Expect(converted.GetFeatures().GetShowMetrics()).To(BeTrue())
			Expect(converted.GetFeatures().GetShowStatus()).To(BeTrue())
			Expect(converted.GetFeatures().GetShowL3Log()).To(BeTrue())
		})
	})

	Context("AgentMode", func() {
		It("should reject unknown mode", func() {
			var mode AgentMode
			err := yaml.Unmarshal([]byte("unknown"), &mode)
			Expect(err).To(MatchError(ContainSubstring("unknown agent mode UNKNOWN")))
		})
		It("should convert a nil mode to the default mode", func() {
			var mode *AgentMode
			Expect(mode.ToAgentMode()).To(Equal(agent.Agent_Mode(0)))
		})
	})
})
