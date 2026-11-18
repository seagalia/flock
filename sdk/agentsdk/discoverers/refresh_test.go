package discoverers_test

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/durationpb"

	. "github.com/seagalia/flock/sdk/agentsdk/discoverers"
)

var _ = Describe("Refresh", func() {
	var (
		mockCtrl  *gomock.Controller
		mockDisco *testhelpers.MockDiscoverer
		states    *stores.StateStore
	)
	BeforeEach(func() {
		states = stores.NewStateStore()
		mockCtrl = gomock.NewController(GinkgoT())
		mockDisco = testhelpers.NewMockDiscoverer(mockCtrl)
	})
	AfterEach(func() {
		mockCtrl.Finish()
	})
	Context("Run", func() {
		It("should return targets by ticks", func() {
			targets := []*core.IPAddr{
				{
					Ip:   "127.0.0.1",
					Port: uint32(8080),
				},
				{
					Ip:   "127.0.0.1",
					Port: uint32(8081),
				},
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			runs := 0
			mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, found chan<- []*core.IPAddr) error {
				found <- targets
				runs++
				if runs == 3 {
					cancel()
				}
				return nil
			}).Times(3)

			found := make(chan []*core.IPAddr)
			sd := NewRefresh(&backend.Backend{UUID: "backend-1", RefreshRate: durationpb.New(10 * time.Millisecond)}, mockDisco, states)
			go func() {
				defer GinkgoRecover()
				err := sd.Run(ctx, found)
				Expect(err).ToNot(HaveOccurred())
				close(found)
			}()

			for foundTargets := range found {
				Expect(foundTargets).To(Equal(targets))
			}
			Expect(runs).To(Equal(3))
			_, _, err := states.Get(helpers.TypeUrl[*backend.Backend](), "backend-1")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should keep the error of the discoverer on its backend while the next refresh runs", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gomock.InOrder(
				mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).Return(fmt.Errorf("dns down")),
				mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ chan<- []*core.IPAddr) error {
					cancel()
					return ctx.Err()
				}),
			)

			backendDef := &backend.Backend{UUID: "backend-err", RefreshRate: durationpb.New(10 * time.Millisecond)}
			err := NewRefresh(backendDef, mockDisco, states).Run(ctx, make(chan []*core.IPAddr))

			Expect(err).ToNot(HaveOccurred())
			_, _, stateErr := states.Get(helpers.TypeUrl[*backend.Backend](), "backend-err")
			Expect(stateErr).To(MatchError(ContainSubstring("dns down")))
		})
	})
})
