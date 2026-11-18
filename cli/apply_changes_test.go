package cli

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
)

var _ = Describe("apply changes helpers", func() {
	Describe("listAll", func() {
		It("asks for pages until an empty one", func() {
			var offsets []int32
			all, err := listAll(func(offset int32) ([]int, error) {
				offsets = append(offsets, offset)
				if offset >= 2*listPageSize {
					return nil, nil
				}
				return []int{int(offset), int(offset) + 1}, nil
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(all).To(Equal([]int{0, 1, 100, 101}))
			Expect(offsets).To(Equal([]int32{0, 100, 200}))
		})

		It("stops on the first error", func() {
			_, err := listAll(func(int32) ([]int, error) { return nil, errors.New("boom") })

			Expect(err).To(MatchError("boom"))
		})
	})

	Describe("sameResource", func() {
		It("is equal when only defaults differ", func() {
			bare := &backend.Backend{Name: "web"}
			defaulted := proto.Clone(bare)
			helpers.ProvDefault(defaulted)

			Expect(sameResource(bare, defaulted)).To(BeTrue())
		})

		It("differs on a changed field", func() {
			Expect(sameResource(&backend.Backend{Name: "web"}, &backend.Backend{Name: "api"})).To(BeFalse())
		})
	})

	Describe("applyFailure", func() {
		It("lists the applied changes in its message", func() {
			failure := &applyFailure{
				Failed:  change{Kind: "vip", Name: "public", Action: actionUpdate},
				Applied: []string{"create backend shop-web"},
				Err:     errors.New("database is down"),
			}

			Expect(failure.Error()).To(Equal("update vip public: database is down\nalready applied:\n  - create backend shop-web"))
			Expect(errors.Unwrap(failure)).To(MatchError("database is down"))
		})

		It("says nothing was applied", func() {
			failure := &applyFailure{Failed: change{Kind: "backend", Name: "web", Action: actionCreate}, Err: errors.New("x")}

			Expect(failure.Error()).To(ContainSubstring("nothing was applied"))
		})
	})
})
