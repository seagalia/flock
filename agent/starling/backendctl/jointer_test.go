package backendctl_test

import (
	"context"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/agent/starling/testhelpers"
	oerrors "github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/types/known/durationpb"

	. "github.com/seagalia/flock/agent/starling/backendctl"
)

type recordingEndpointListener struct {
	mutex     sync.Mutex
	endpoints map[string]bool
	startups  int
}

func newRecordingEndpointListener() *recordingEndpointListener {
	return &recordingEndpointListener{endpoints: make(map[string]bool)}
}

func (l *recordingEndpointListener) OnStartup(_ *backend.Backend, ipAddresses []*core.IPAddr) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.startups++
	for _, ipAddr := range ipAddresses {
		l.endpoints[ipAddr.Format()] = true
	}
}

func (l *recordingEndpointListener) OnEndpointAdded(_ *backend.Backend, ipAddr *core.IPAddr) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.endpoints[ipAddr.Format()] = true
}

func (l *recordingEndpointListener) OnEndpointRemoved(_ *backend.Backend, ipAddr *core.IPAddr) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	delete(l.endpoints, ipAddr.Format())
}

func (l *recordingEndpointListener) Endpoints() []string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	endpoints := make([]string, 0, len(l.endpoints))
	for endpoint := range l.endpoints {
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func (l *recordingEndpointListener) Startups() int {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return l.startups
}

func staticBackend(uuid string, ports ...uint32) *backend.Backend {
	ipAddrs := make([]*core.IPAddr, len(ports))
	for i, port := range ports {
		ipAddrs[i] = &core.IPAddr{Ip: "127.0.0.1", Port: port}
	}
	return &backend.Backend{
		UUID:           uuid,
		Name:           uuid,
		ConnectTimeout: durationpb.New(time.Second),
		LbPolicy:       backend.Backend_ROUND_ROBIN,
		Endpoint: &backend.Backend_IpAddrGroup{
			IpAddrGroup: &core.IPAddrGroup{IpAddresses: ipAddrs},
		},
		HealthCheck: &core.HealthCheck{
			Timeout:  durationpb.New(time.Second),
			Interval: durationpb.New(10 * time.Millisecond),
			HealthChecker: &core.HealthCheck_NoHealthCheck{
				NoHealthCheck: &core.NoHealthCheck{},
			},
		},
		RefreshRate: durationpb.New(10 * time.Millisecond),
	}
}

var _ = Describe("Joint", func() {
	var (
		mockCtrl      *gomock.Controller
		joint         *Joint
		jointListener *testhelpers.MockJointerListener
		ctx           = context.Background()
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		jointListener = testhelpers.NewMockJointerListener(mockCtrl)
		joint = NewJoint(context.Background(), stores.NewStateStore())
	})

	AfterEach(func() {
		Expect(joint.Stop()).To(Succeed())
	})

	It("should give its endpoints to a listener added before the backend", func() {
		jointListener.EXPECT().OnBackendAdded(gomock.Any()).AnyTimes()
		joint.AddListener("backend-1", jointListener)
		edpListener := newRecordingEndpointListener()
		joint.AddEndpointListener("backend-1", edpListener)

		Expect(joint.OnMessageAdded(ctx, staticBackend("backend-1", 8080))).To(Succeed())

		Eventually(edpListener.Endpoints).Should(ConsistOf("127.0.0.1:8080"))
	})

	It("should not give endpoints to a listener removed before the backend", func() {
		jointListener.EXPECT().OnBackendAdded(gomock.Any()).AnyTimes()
		joint.AddListener("backend-1", jointListener)
		edpListener := newRecordingEndpointListener()
		joint.AddEndpointListener("backend-1", edpListener)
		Expect(joint.RemoveEndpointListener("backend-1", edpListener)).To(Succeed())

		Expect(joint.OnMessageAdded(ctx, staticBackend("backend-1", 8080))).To(Succeed())

		Consistently(edpListener.Startups, 200*time.Millisecond).Should(BeZero())
		Expect(oerrors.IsErrNotFound(joint.RemoveEndpointListener("unknown", edpListener))).To(BeTrue())
	})

	It("should notify backend added and resolve its endpoints", func() {
		aBackend := staticBackend("backend-1", 8080, 8081)
		added := make(chan struct{})
		jointListener.EXPECT().OnBackendAdded(gomock.Any()).Do(func(c *backend.Backend) {
			defer GinkgoRecover()
			Expect(c.GetUUID()).To(Equal("backend-1"))
			close(added)
		})
		joint.AddListener("backend-1", jointListener)
		// adding twice the same listener must not duplicate notifications
		joint.AddListener("backend-1", jointListener)

		Expect(joint.OnMessageAdded(ctx, aBackend)).To(Succeed())
		Eventually(added).Should(BeClosed())

		edpListener := newRecordingEndpointListener()
		joint.AddEndpointListener("backend-1", edpListener)
		Eventually(edpListener.Endpoints).Should(ConsistOf("127.0.0.1:8080", "127.0.0.1:8081"))

		Eventually(func() bool {
			stat := joint.EndpointStatsForBackend("backend-1")
			return stat != nil && stat.GetHealthy() && len(stat.GetEndpointStats()) == 2
		}).Should(BeTrue())
		Expect(joint.EndpointStatsForBackend("unknown")).To(BeNil())

		allStats := joint.EndpointStatsByBackends()
		Expect(allStats).To(HaveLen(1))
		Expect(allStats[0].GetBackendUuid()).To(Equal("backend-1"))
	})

	It("should notify backend updated and move endpoint listeners to the new resolver", func() {
		updated := make(chan struct{})
		jointListener.EXPECT().OnBackendAdded(gomock.Any())
		jointListener.EXPECT().OnBackendUpdated(gomock.Any()).Do(func(c *backend.Backend) {
			close(updated)
		})
		joint.AddListener("backend-1", jointListener)

		Expect(joint.OnMessageAdded(ctx, staticBackend("backend-1", 8080))).To(Succeed())
		edpListener := newRecordingEndpointListener()
		joint.AddEndpointListener("backend-1", edpListener)
		Eventually(edpListener.Endpoints).Should(ConsistOf("127.0.0.1:8080"))

		Expect(joint.OnMessageAdded(ctx, staticBackend("backend-1", 8080, 9090))).To(Succeed())
		Eventually(updated).Should(BeClosed())

		Eventually(edpListener.Endpoints).Should(ConsistOf("127.0.0.1:8080", "127.0.0.1:9090"))
		Expect(edpListener.Startups()).To(Equal(2), "listener must be re-registered on the new resolver")
	})

	It("should notify backend removed and forget the backend", func() {
		removed := make(chan struct{})
		jointListener.EXPECT().OnBackendAdded(gomock.Any())
		jointListener.EXPECT().OnBackendRemoved(gomock.Any()).Do(func(c *backend.Backend) {
			close(removed)
		})
		joint.AddListener("backend-1", jointListener)

		aBackend := staticBackend("backend-1", 8080)
		Expect(joint.OnMessageAdded(ctx, aBackend)).To(Succeed())
		Expect(joint.OnMessageRemove(ctx, aBackend)).To(Succeed())
		Eventually(removed).Should(BeClosed())

		Expect(joint.EndpointStatsByBackends()).To(BeEmpty())

		// removing an unknown backend is a no-op
		Expect(joint.OnMessageRemove(ctx, staticBackend("unknown"))).To(Succeed())
	})

	It("should not notify a removed listener", func() {
		joint.AddListener("backend-1", jointListener)
		joint.RemoveListener("backend-1", jointListener)
		// removing an unknown listener is a no-op
		joint.RemoveListener("backend-1", jointListener)

		// a second listener proves notification was sent while removed one is never called
		otherListener := testhelpers.NewMockJointerListener(mockCtrl)
		notified := make(chan struct{})
		otherListener.EXPECT().OnBackendAdded(gomock.Any()).Do(func(c *backend.Backend) {
			close(notified)
		})
		joint.AddListener("backend-1", otherListener)

		Expect(joint.OnMessageAdded(ctx, staticBackend("backend-1", 8080))).To(Succeed())
		Eventually(notified).Should(BeClosed())
	})

	It("should stop every resolvers on stop", func() {
		jointListener.EXPECT().OnBackendAdded(gomock.Any()).AnyTimes()
		joint.AddListener("backend-1", jointListener)
		Expect(joint.OnMessageAdded(ctx, staticBackend("backend-1", 8080))).To(Succeed())
		Expect(joint.OnMessageAdded(ctx, staticBackend("backend-2", 8080))).To(Succeed())

		Expect(joint.Stop()).To(Succeed())
		Expect(joint.EndpointStatsByBackends()).To(BeEmpty())
	})

	It("should update a backend whose discoverer failed to start without freezing the others", func() {
		jointListener.EXPECT().OnBackendAdded(gomock.Any()).AnyTimes()
		jointListener.EXPECT().OnBackendUpdated(gomock.Any()).AnyTimes()
		joint.AddListener("broken", jointListener)
		joint.AddListener("healthy", jointListener)
		// no endpoint type, so MakeDiscoverer refuses it and Run returns at once
		broken := staticBackend("broken")
		broken.Endpoint = nil
		broken.RefreshRate = durationpb.New(time.Minute)
		Expect(joint.OnMessageAdded(ctx, staticBackend("healthy", 8080))).To(Succeed())
		Expect(joint.OnMessageAdded(ctx, broken)).To(Succeed())

		updated := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			updated <- joint.OnMessageAdded(ctx, broken)
		}()
		Eventually(updated, 2*time.Second).Should(Receive(Succeed()))

		joint.AddEndpointListener("healthy", newRecordingEndpointListener())
	})
})
