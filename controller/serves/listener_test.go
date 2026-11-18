package serves_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/controller/announcers"
	. "github.com/seagalia/flock/controller/serves"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"go.uber.org/mock/gomock"
)

var _ = Describe("Listener", func() {
	var (
		mockCtrl *gomock.Controller
		mockAds  *adsServerMock
		listener *DiscoAnList
		doneEmit chan *DoneEmit
		errEmit  chan *ErrEmit
	)
	BeforeEach(func() {
		doneEmit = make(chan *DoneEmit)
		errEmit = make(chan *ErrEmit)
		mockCtrl = gomock.NewController(GinkgoT())
		mockAds = newAdsServerMock(mockCtrl)
		listener = NewDiscoAnList(context.Background(), mockAds, doneEmit, errEmit)

	})
	AfterEach(func() {
		mockCtrl.Finish()
		close(doneEmit)
	})
	Context("OnAnnounce", func() {
		When("receive change event", func() {
			It("should send changed resources", func() {
				mockAds.EXPECT().Send(gomock.Any()).DoAndReturn(func(resp *svcdiscovery.DeltaDiscoveryResponse) error {
					Expect(resp.RemovedResources).To(BeEmpty())
					Expect(resp.Resources).To(HaveLen(1))
					Expect(resp.Resources[0].GetUuid()).To(Equal("uuid"))
					return nil
				}).Times(1)
				mockAds.EXPECT().Receive().DoAndReturn(func() (*svcdiscovery.DeltaDiscoveryRequest, error) {
					return &svcdiscovery.DeltaDiscoveryRequest{
						ResponseNonce: "1",
					}, nil
				}).Times(1)
				listener.OnAnnounce(announcers.ChangeEvent, []*svcdiscovery.Resource{
					{
						Uuid:      "uuid",
						Signature: "sig",
					},
				})

			})
		})
		When("receive remove event", func() {
			It("should send remove elements", func() {
				mockAds.EXPECT().Send(gomock.Any()).DoAndReturn(func(resp *svcdiscovery.DeltaDiscoveryResponse) error {
					Expect(resp.RemovedResources).To(HaveLen(1))
					Expect(resp.RemovedResources[0]).To(Equal("uuid"))
					Expect(resp.Resources).To(BeEmpty())
					return nil
				}).Times(1)
				mockAds.EXPECT().Receive().DoAndReturn(func() (*svcdiscovery.DeltaDiscoveryRequest, error) {
					return &svcdiscovery.DeltaDiscoveryRequest{
						ResponseNonce: "1",
					}, nil
				}).Times(1)
				listener.OnAnnounce(announcers.RemoveEvent, []*svcdiscovery.Resource{
					{
						Uuid:      "uuid",
						Signature: "sig",
					},
				})

			})
		})
		When("send error", func() {
			It("should send error on done channel", func() {
				expectedErr := fmt.Errorf("finished connection")
				mockAds.EXPECT().Send(gomock.Any()).Return(expectedErr)
				mockAds.EXPECT().Receive().DoAndReturn(func() (*svcdiscovery.DeltaDiscoveryRequest, error) {
					return &svcdiscovery.DeltaDiscoveryRequest{
						ResponseNonce: "1",
					}, nil
				}).AnyTimes()
				go func() {
					defer GinkgoRecover()
					listener.OnAnnounce(announcers.ChangeEvent, []*svcdiscovery.Resource{
						{
							Uuid:      "uuid",
							Signature: "sig",
						},
					})
				}()
				done := <-doneEmit
				Expect(done.Err).Should(Equal(expectedErr))
			})
		})
		When("the agent half-closes the stream while the ack is pending", func() {
			It("should end the stream cleanly with io.EOF on done channel", func() {
				mockAds.EXPECT().Send(gomock.Any()).Return(nil).Times(1)
				mockAds.EXPECT().Receive().Return(nil, io.EOF).Times(1)
				announced := make(chan struct{})
				go func() {
					defer GinkgoRecover()
					defer close(announced)
					listener.OnAnnounce(announcers.ChangeEvent, []*svcdiscovery.Resource{
						{
							Uuid:      "uuid",
							Signature: "sig",
						},
					})
				}()
				var done *DoneEmit
				Eventually(doneEmit).Should(Receive(&done))
				Expect(done.Err).Should(MatchError(io.EOF))
				Eventually(announced).Should(BeClosed())
			})
		})
		When("the stream already ended", func() {
			It("does not panic or block when announcing after the stream ended", func() {
				endedCtx, cancel := context.WithCancel(context.Background())
				cancel()
				endedAds := newAdsServerMock(mockCtrl)
				endedAds.EXPECT().Send(gomock.Any()).Return(nil).AnyTimes()
				endedAds.EXPECT().Receive().Return(nil, errors.New("transport closed")).AnyTimes()
				endedListener := NewDiscoAnList(endedCtx, endedAds, make(chan *DoneEmit), make(chan *ErrEmit))
				announced := make(chan struct{})
				go func() {
					defer GinkgoRecover()
					defer close(announced)
					endedListener.OnAnnounce(announcers.ChangeEvent, []*svcdiscovery.Resource{{Uuid: "uuid"}})
				}()
				Eventually(announced, 2*time.Second).Should(BeClosed())
			})
		})
		When("sending to the agent fails", func() {
			It("reports a send failure once and returns without waiting for the ack", func() {
				sendErr := errors.New("send broke")
				releaseRecv := make(chan struct{})
				DeferCleanup(func() { close(releaseRecv) })
				failingAds := newAdsServerMock(mockCtrl)
				failingAds.EXPECT().Send(gomock.Any()).Return(sendErr).Times(1)
				failingAds.EXPECT().Receive().DoAndReturn(func() (*svcdiscovery.DeltaDiscoveryRequest, error) {
					<-releaseRecv
					return nil, io.EOF
				}).AnyTimes()
				bufferedDone := make(chan *DoneEmit, 1)
				failingListener := NewDiscoAnList(context.Background(), failingAds, bufferedDone, make(chan *ErrEmit))
				announced := make(chan struct{})
				go func() {
					defer GinkgoRecover()
					defer close(announced)
					failingListener.OnAnnounce(announcers.ChangeEvent, []*svcdiscovery.Resource{{Uuid: "uuid"}})
				}()
				Eventually(announced, 2*time.Second).Should(BeClosed())
				Expect(bufferedDone).To(HaveLen(1))
				Expect((<-bufferedDone).Err).To(MatchError(sendErr))
				Consistently(bufferedDone).ShouldNot(Receive())
			})
		})
		When("the init discoverer fails while done already holds a result", func() {
			It("signals waitInit without waiting for the done reader", func() {
				failingAds := newAdsServerMock(mockCtrl)
				failingAds.EXPECT().Send(gomock.Any()).Return(errors.New("send broke")).Times(1)
				fullDone := make(chan *DoneEmit, 1)
				fullDone <- &DoneEmit{Err: errors.New("filled by OnAnnounce")}
				waitInit := make(chan struct{}, 1)
				initDiscoverer := DefaultInitDiscoverer(context.Background(), zerolog.Nop(), failingAds, waitInit, fullDone)
				initReturned := make(chan struct{})
				go func() {
					defer GinkgoRecover()
					defer close(initReturned)
					initDiscoverer([]*svcdiscovery.Resource{{Uuid: "uuid"}}, nil)
				}()
				Eventually(waitInit, 2*time.Second).Should(Receive())
				Eventually(fullDone).Should(Receive())
				Eventually(initReturned, 2*time.Second).Should(BeClosed())
			})
		})
	})
})
