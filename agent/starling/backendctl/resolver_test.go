package backendctl_test

import (
	"context"
	"fmt"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ArthurHlt/gohc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/agent/starling/testhelpers"
	agtesthelpers "github.com/seagalia/flock/sdk/agentsdk/testhelpers"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/durationpb"

	. "github.com/seagalia/flock/agent/starling/backendctl"
)

var _ = Describe("Backend", func() {
	var (
		mockCtrl        *gomock.Controller
		mockHc          *testhelpers.MockHealthChecker
		mockDisco       *agtesthelpers.MockDiscoverer
		mockListener    *testhelpers.MockEndpointListener
		backendDef      *backend.Backend
		backendResolver *Resolver
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mockHc = testhelpers.NewMockHealthChecker(mockCtrl)
		mockDisco = agtesthelpers.NewMockDiscoverer(mockCtrl)
		mockListener = testhelpers.NewMockEndpointListener(mockCtrl)
		mockListener.EXPECT().OnStartup(gomock.Any(), gomock.Any()).AnyTimes()
		backendDef = &backend.Backend{
			UUID:           "a-backend",
			Name:           "a-backend",
			EnableTls:      false,
			Ca:             "",
			ConnectTimeout: durationpb.New(10 * time.Second),
			LbPolicy:       backend.Backend_ROUND_ROBIN,
			HealthCheck: &core.HealthCheck{
				Timeout:            durationpb.New(10 * time.Second),
				Interval:           durationpb.New(5 * time.Second),
				UnhealthyThreshold: 3,
				HealthyThreshold:   3,
				AltPort:            0,
				HealthChecker: &core.HealthCheck_TcpHealthCheck{
					TcpHealthCheck: &core.TcpHealthCheck{},
				},
				UnhealthyInterval: durationpb.New(5 * time.Second),
			},
			RefreshRate: durationpb.New(10 * time.Millisecond),
		}
		backendResolver = NewResolver(backendDef, stores.NewStateStore())
		setMockResolver(backendResolver, mockHc, mockDisco)
		backendResolver.AddListener(mockListener)
	})
	AfterEach(func() {
		backendResolver.RemoveListener(mockListener)
		mockCtrl.Finish()
	})
	Context("Run", func() {
		It("should discovers endpoint, set healthcheck and announce healthy/unhealthy endpoints", func() {
			mockHc.EXPECT().Check(gomock.Any()).Return(nil).MinTimes(1)
			mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, found chan<- []*core.IPAddr) error {
				found <- []*core.IPAddr{
					{
						Ip:   "127.0.0.1",
						Port: uint32(8080),
					},
					{
						Ip:   "127.0.0.1",
						Port: uint32(8081),
					},
				}
				return nil
			}).MinTimes(1)
			executed := &atomic.Int64{}

			mockListener.EXPECT().OnEndpointAdded(gomock.Any(), gomock.Any()).DoAndReturn(func(backendDef *backend.Backend, ipAddr *core.IPAddr) {
				defer GinkgoRecover()
				Expect(ipAddr.GetIp()).To(Equal("127.0.0.1"))
				Expect(ipAddr.GetPort()).To(Or(Equal(uint32(8080)), Equal(uint32(8081))))
				executed.Add(1)
			}).Times(2)

			ctx, cancelFunc := context.WithCancel(context.Background())
			defer func() {
				cancelFunc()
				<-backendResolver.Done()
			}()

			go func() {
				defer GinkgoRecover()
				err := backendResolver.Run(ctx)
				Expect(err).ToNot(HaveOccurred())
			}()

			Eventually(func() int64 {
				return executed.Load()
			}).Should(Equal(int64(2)))
			Expect(backendResolver.IsHealthy()).To(BeTrue())
		})
		It("should announce removed a healthy endpoint the discoverer stops giving", func() {
			mockHc.EXPECT().Check(gomock.Any()).Return(nil).MinTimes(1)
			discoveries := make(chan []*core.IPAddr)
			mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, found chan<- []*core.IPAddr) error {
				for {
					select {
					case ips := <-discoveries:
						found <- ips
					case <-ctx.Done():
						return nil
					}
				}
			}).Times(1)
			added := &atomic.Int64{}
			mockListener.EXPECT().OnEndpointAdded(gomock.Any(), gomock.Any()).Do(func(*backend.Backend, *core.IPAddr) {
				added.Add(1)
			}).Times(2)
			removed := make(chan *core.IPAddr, 1)
			mockListener.EXPECT().OnEndpointRemoved(gomock.Any(), gomock.Any()).Do(func(_ *backend.Backend, ipAddr *core.IPAddr) {
				removed <- ipAddr
			}).Times(1)
			backendDef.HealthCheck.HealthyThreshold = 1

			ctx, cancelFunc := context.WithCancel(context.Background())
			defer func() {
				cancelFunc()
				<-backendResolver.Done()
			}()
			go func() {
				defer GinkgoRecover()
				Expect(backendResolver.Run(ctx)).To(Succeed())
			}()

			discoveries <- []*core.IPAddr{{Ip: "127.0.0.1", Port: uint32(8080)}, {Ip: "127.0.0.1", Port: uint32(8081)}}
			Eventually(added.Load).Should(Equal(int64(2)))
			discoveries <- []*core.IPAddr{{Ip: "127.0.0.1", Port: uint32(8080)}}
			Eventually(removed).Should(Receive(HaveField("Port", uint32(8081))))
			Expect(backendResolver.HealthyEndpoints()).To(ConsistOf(HaveField("Port", uint32(8080))))
		})
		It("should stop within a second of the cancel whatever the refresh rate of a failing discoverer", func() {
			backendDef.RefreshRate = durationpb.New(time.Minute)
			mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, found chan<- []*core.IPAddr) error {
				return fmt.Errorf("discovery failed")
			}).MinTimes(1)
			ctx, cancelFunc := context.WithCancel(context.Background())
			returned := make(chan error, 1)
			go func() {
				defer GinkgoRecover()
				returned <- backendResolver.Run(ctx)
			}()
			time.Sleep(100 * time.Millisecond)
			cancelFunc()
			Eventually(returned, time.Second).Should(Receive(Succeed()))
		})
		When("endpoint is unhealthy", func() {
			It("should ignore unhealthy endpoint and mark backend as unhealthy", func() {
				mockHc.EXPECT().Check(gomock.Any()).DoAndReturn(func(ipAddr string) error {
					if strings.HasSuffix(ipAddr, ":8081") {
						return fmt.Errorf("unhealthy")
					}
					return nil
				}).MinTimes(1)
				mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, found chan<- []*core.IPAddr) error {
					found <- []*core.IPAddr{
						{
							Ip:   "127.0.0.1",
							Port: uint32(8080),
						},
						{
							Ip:   "127.0.0.1",
							Port: uint32(8081),
						},
					}
					return nil
				}).MinTimes(1)
				executed := &atomic.Int64{}

				mockListener.EXPECT().OnEndpointAdded(gomock.Any(), gomock.Any()).DoAndReturn(func(backendDef *backend.Backend, ipAddr *core.IPAddr) {
					defer GinkgoRecover()
					Expect(ipAddr.GetIp()).To(Equal("127.0.0.1"))
					Expect(ipAddr.GetPort()).To(Equal(uint32(8080)))
					executed.Add(1)
				}).Times(1)

				ctx, cancelFunc := context.WithCancel(context.Background())
				defer func() {
					cancelFunc()
					<-backendResolver.Done()
				}()

				go func() {
					defer GinkgoRecover()
					err := backendResolver.Run(ctx)
					Expect(err).ToNot(HaveOccurred())
				}()

				Eventually(func() int64 {
					return executed.Load()
				}).Should(Equal(int64(1)))
				Expect(backendResolver.IsHealthy()).To(BeFalse())
			})
		})
	})
	Context("health checks", func() {
		var (
			added  *atomic.Int64
			cancel context.CancelFunc
		)
		runWith := func(endpointCount int) {
			mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, found chan<- []*core.IPAddr) error {
				ips := make([]*core.IPAddr, endpointCount)
				for n := range ips {
					ips[n] = &core.IPAddr{Ip: "127.0.0.1", Port: uint32(10000 + n)}
				}
				found <- ips
				return nil
			}).MinTimes(1)
			mockListener.EXPECT().OnEndpointAdded(gomock.Any(), gomock.Any()).Do(func(*backend.Backend, *core.IPAddr) {
				added.Add(1)
			}).Times(endpointCount)
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			go func() {
				defer GinkgoRecover()
				Expect(backendResolver.Run(ctx)).To(Succeed())
			}()
		}
		BeforeEach(func() {
			added = &atomic.Int64{}
			backendDef.HealthCheck.HealthyThreshold = 1
			backendDef.HealthCheck.Interval = durationpb.New(time.Hour)
			mockHc.EXPECT().Check(gomock.Any()).Return(nil).AnyTimes()
		})
		AfterEach(func() {
			cancel()
			Eventually(backendResolver.Done()).Should(BeClosed())
		})

		It("should build one health checker for all the endpoints of the backend", func() {
			built := &atomic.Int64{}
			backendResolver.SetHealthCheckFactory(func(*core.HealthCheck, string, string) (gohc.HealthChecker, error) {
				built.Add(1)
				return mockHc, nil
			})
			runWith(2)

			Eventually(added.Load).Should(Equal(int64(2)))
			Expect(built.Load()).To(Equal(int64(1)))
		})

		It("should hold no goroutine per endpoint between two checks", func() {
			before := runtime.NumGoroutine()
			runWith(200)

			Eventually(added.Load).Should(Equal(int64(200)))
			Eventually(func() int { return runtime.NumGoroutine() - before }).Should(BeNumerically("<", 20))
		})
	})
	Context("EndpointStats", func() {
		It("give stats from all endpoints", func() {
			mockHc.EXPECT().Check(gomock.Any()).DoAndReturn(func(ipAddr string) error {
				if strings.HasSuffix(ipAddr, ":8081") {
					return fmt.Errorf("unhealthy")
				}
				return nil
			}).MinTimes(1)
			mockDisco.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, found chan<- []*core.IPAddr) error {
				found <- []*core.IPAddr{
					{
						Ip:   "127.0.0.1",
						Port: uint32(8080),
					},
					{
						Ip:   "127.0.0.1",
						Port: uint32(8081),
					},
				}
				return nil
			}).MinTimes(1)
			executed := &atomic.Int64{}

			mockListener.EXPECT().OnEndpointAdded(gomock.Any(), gomock.Any()).DoAndReturn(func(backendDef *backend.Backend, ipAddr *core.IPAddr) {
				defer GinkgoRecover()
				Expect(ipAddr.GetIp()).To(Equal("127.0.0.1"))
				Expect(ipAddr.GetPort()).To(Equal(uint32(8080)))
				executed.Add(1)
			}).Times(1)

			ctx, cancelFunc := context.WithCancel(context.Background())
			defer func() {
				cancelFunc()
				<-backendResolver.Done()
			}()

			go func() {
				defer GinkgoRecover()
				err := backendResolver.Run(ctx)
				Expect(err).ToNot(HaveOccurred())
			}()

			Eventually(func() int64 {
				return executed.Load()
			}).Should(Equal(int64(1)))

			endpointStats := backendResolver.EndpointStats()
			Expect(endpointStats).To(HaveLen(2))
			Expect(endpointStats[0].IpAddr.GetIp()).To(Equal("127.0.0.1"))
			if endpointStats[0].IpAddr.GetPort() == 8080 {
				Expect(endpointStats[0].Healthy).To(BeTrue())
				Expect(endpointStats[0].LastError).To(BeNil())
				Expect(endpointStats[1].Healthy).To(BeFalse())
				Expect(endpointStats[1].LastError).ToNot(BeNil())
				Expect(endpointStats[1].LastError.GetValue()).To(ContainSubstring("healthcheck not yet started"))

			} else {
				Expect(endpointStats[0].Healthy).To(BeFalse())
				Expect(endpointStats[0].LastError).ToNot(BeNil())
				Expect(endpointStats[0].LastError.GetValue()).To(ContainSubstring("healthcheck not yet started"))
				Expect(endpointStats[1].Healthy).To(BeTrue())
				Expect(endpointStats[1].LastError).To(BeNil())
			}
		})
	})
})
