package svccore_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	. "github.com/seagalia/flock/sdk/flock/services/core/v1"
)

var _ = Describe("ListFilter", func() {
	production := &zone.Zone{UUID: "zone-1", Name: "production", Metadata: map[string]string{"site": "paris"}}

	DescribeTable("should match a message on its name, uuid or metadata",
		func(filter *ListFilter, expected bool) {
			Expect(filter.Match(production)).To(Equal(expected))
		},
		Entry("equal name", &ListFilter{Key: "name", Value: "production"}, true),
		Entry("other name", &ListFilter{Key: "name", Value: "staging"}, false),
		Entry("not equal name", &ListFilter{Key: "name", Value: "staging", Operator: ListFilter_NOT_EQUAL}, true),
		Entry("like name", &ListFilter{Key: "name", Value: "prod", Operator: ListFilter_LIKE}, true),
		Entry("not like name", &ListFilter{Key: "name", Value: "prod", Operator: ListFilter_NOT_LIKE}, false),
		Entry("uuid", &ListFilter{Key: "uuid", Value: "zone-1"}, true),
		Entry("metadata", &ListFilter{Key: "site", Value: "paris", IsMetadata: true}, true),
		Entry("other metadata value", &ListFilter{Key: "site", Value: "lyon", IsMetadata: true}, false),
		Entry("missing metadata key", &ListFilter{Key: "team", Value: "paris", IsMetadata: true}, false),
		Entry("metadata key is not a field", &ListFilter{Key: "site", Value: "paris"}, false),
	)

	It("should match all or any filters", func() {
		filters := ListFilters{
			{Key: "name", Value: "production"},
			{Key: "site", Value: "lyon", IsMetadata: true},
		}
		Expect(filters.MatchAnd(production)).To(BeFalse())
		Expect(filters.MatchOr(production)).To(BeTrue())

		var none ListFilters
		Expect(none.MatchAnd(production)).To(BeTrue())
		Expect(none.MatchOr(production)).To(BeTrue())
	})
})
