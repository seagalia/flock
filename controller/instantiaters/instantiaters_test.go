package instantiaters_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/dbs"
	. "github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
)

func expectRunning(status *instance.InstanceStatus, err error) {
	Expect(err).ToNot(HaveOccurred())
	Expect(status.GetCurrentState()).To(Equal(instance.InstanceStatus_RUNNING))
	Expect(status.GetDesiredState()).To(Equal(instance.InstanceStatus_RUNNING))
	Expect(status.GetErrorMessage()).To(BeEmpty())
}

var _ = Describe("KnownInstantiator", func() {
	var known *KnownInstantiator
	BeforeEach(func() {
		known = &KnownInstantiator{
			Name:          "known",
			Zones:         []string{"z1", "z2"},
			ResourceTypes: []*config.ResourceType{{Name: "small"}, {Name: "large"}},
		}
	})

	DescribeTable("should instantiate supported resource type in supported zone",
		func(params InstanceParams) {
			Expect(known.CanInstantiate(params)).To(BeTrue())
		},
		Entry("matching zone", InstanceParams{ResourceTypeName: "large", Zone: "z2"}),
		Entry("no zone requested", InstanceParams{ResourceTypeName: "small"}),
	)
	It("should instantiate in any zone when instantiator has no zone", func() {
		known.Zones = nil
		Expect(known.CanInstantiate(InstanceParams{ResourceTypeName: "small", Zone: "z3"})).To(BeTrue())
	})
	DescribeTable("should refuse",
		func(params InstanceParams) {
			ok, err := known.CanInstantiate(params)
			Expect(ok).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("not supported by instantiator known")))
		},
		Entry("unknown resource type", InstanceParams{ResourceTypeName: "medium", Zone: "z1"}),
		Entry("unknown zone", InstanceParams{ResourceTypeName: "small", Zone: "z3"}),
	)
})

var _ = Describe("DbMulti", func() {
	var multi *DbMulti
	BeforeEach(func() {
		var err error
		multi, err = NewDbMulti(context.Background(), dbMan, []*config.Instantiator{
			{
				Name:                "static",
				Zones:               []string{"z1"},
				OriginatingIdentity: "controller",
				ResourceTypes:       []*config.ResourceType{{Name: "small"}},
				StaticInstantiator: &config.StaticInstantiator{
					Agents: []*config.AgentInstantiator{{UUID: "agent-1", Name: "agent-1", Host: "host-1:443"}},
				},
			},
			{
				Name:                "static-2",
				Zones:               []string{"z2"},
				OriginatingIdentity: "controller",
				ResourceTypes:       []*config.ResourceType{{Name: "small"}},
				StaticInstantiator: &config.StaticInstantiator{
					Agents: []*config.AgentInstantiator{{UUID: "agent-2", Name: "agent-2", Host: "host-2:443"}},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = dbMan.Exec(context.Background(), dbs.MakeDbQuery("DELETE FROM service_instances", nil))
		Expect(err).ToNot(HaveOccurred())
	})
	staticParams := InstanceParams{InstanceUUID: "inst-1", Zone: "z1", ResourceTypeName: "small"}

	It("should not record an instance on a cancelled ctx", func() {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := multi.CreateServiceInstance(cancelled, staticParams)

		Expect(err).To(MatchError(context.Canceled))
		_, err = multi.GetStatus(context.Background(), staticParams.InstanceUUID)
		Expect(err).To(MatchError(ContainSubstring("not found")))
	})

	Context("CanInstantiate", func() {
		It("should accept when one instantiator can instantiate", func() {
			Expect(multi.CanInstantiate(context.Background(), InstanceParams{Zone: "z2", ResourceTypeName: "small"})).To(BeTrue())
		})
		It("should give errors of all instantiators when none can instantiate", func() {
			ok, err := multi.CanInstantiate(context.Background(), InstanceParams{Zone: "z3", ResourceTypeName: "small"})
			Expect(ok).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("instantiator static")))
			Expect(err).To(MatchError(ContainSubstring("instantiator static-2")))
		})
	})

	Context("CreateServiceInstance", func() {
		It("should create instance with instantiator matching zone", func() {
			expectRunning(multi.CreateServiceInstance(context.Background(), staticParams))

			agents, err := multi.ListAgents(context.Background(), "inst-1")
			Expect(err).ToNot(HaveOccurred())
			Expect(agents).To(HaveLen(1))
			Expect(agents[0].GetUUID()).To(Equal("agent-1"))
		})
		It("should use the instantiator of its zone", func() {
			expectRunning(multi.CreateServiceInstance(context.Background(), InstanceParams{InstanceUUID: "inst-2", Zone: "z2", ResourceTypeName: "small"}))

			agents, err := multi.ListAgents(context.Background(), "inst-2")
			Expect(err).ToNot(HaveOccurred())
			Expect(agents).To(HaveLen(1))
			Expect(agents[0].GetUUID()).To(Equal("agent-2"))
		})
		It("should refuse to create an existing instance", func() {
			expectRunning(multi.CreateServiceInstance(context.Background(), staticParams))

			_, err := multi.CreateServiceInstance(context.Background(), staticParams)
			Expect(err).To(MatchError(ContainSubstring("service instance inst-1 already exists")))
		})
		It("should refuse when no instantiator matches", func() {
			_, err := multi.CreateServiceInstance(context.Background(), InstanceParams{InstanceUUID: "inst-1", Zone: "z3", ResourceTypeName: "small"})
			Expect(err).To(MatchError(ContainSubstring("no instantiator found for instance inst-1")))
		})
	})

	Context("existing instance", func() {
		BeforeEach(func() {
			expectRunning(multi.CreateServiceInstance(context.Background(), staticParams))
		})
		It("should update instance", func() {
			expectRunning(multi.UpdateServiceInstance(context.Background(), staticParams))
		})
		It("should refuse to move instance to another instantiator", func() {
			_, err := multi.UpdateServiceInstance(context.Background(), InstanceParams{InstanceUUID: "inst-1", Zone: "z2", ResourceTypeName: "small"})
			Expect(err).To(MatchError(ContainSubstring("cannot change instantiator for instance inst-1")))
		})
		It("should give status", func() {
			expectRunning(multi.GetStatus(context.Background(), "inst-1"))
		})
		It("should delete instance", func() {
			expectRunning(multi.DeleteServiceInstance(context.Background(), "inst-1"))

			_, err := multi.ListAgents(context.Background(), "inst-1")
			Expect(err).To(MatchError(ContainSubstring("service instance inst-1 not found")))
		})
	})

	Context("unknown instance", func() {
		It("should return not found errors", func() {
			_, err := multi.ListAgents(context.Background(), "unknown")
			Expect(err).To(MatchError(ContainSubstring("service instance unknown not found")))
			_, err = multi.UpdateServiceInstance(context.Background(), InstanceParams{InstanceUUID: "unknown"})
			Expect(err).To(MatchError(ContainSubstring("service instance unknown not found")))
			_, err = multi.DeleteServiceInstance(context.Background(), "unknown")
			Expect(err).To(MatchError(ContainSubstring("service instance unknown not found")))
			_, err = multi.GetStatus(context.Background(), "unknown")
			Expect(err).To(MatchError(ContainSubstring("service instance unknown not found")))
		})
	})

	Context("instantiator removed from config", func() {
		It("should tell instantiator of instance is not found", func() {
			expectRunning(multi.CreateServiceInstance(context.Background(), staticParams))
			withoutStatic, err := NewDbMulti(context.Background(), dbMan, []*config.Instantiator{{
				Name:          "static-2",
				ResourceTypes: []*config.ResourceType{{Name: "small"}},
				StaticInstantiator: &config.StaticInstantiator{
					Agents: []*config.AgentInstantiator{{UUID: "agent-2", Name: "agent-2", Host: "host-2:443"}},
				},
			}})
			Expect(err).ToNot(HaveOccurred())

			_, err = withoutStatic.GetStatus(context.Background(), "inst-1")
			Expect(err).To(MatchError(ContainSubstring("no instantiator found with name 'static'")))
		})
	})
})
