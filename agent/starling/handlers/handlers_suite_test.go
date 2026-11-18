package handlers_test

import (
	"context"
	"github.com/seagalia/flock/sdk/agentsdk/runtime"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/stream/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/logging"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/seagalia/flock/agent/starling/envoyctl"
	"github.com/seagalia/flock/agent/starling/ipvsctl"
)

func TestHandlers(t *testing.T) {
	RegisterFailHandler(Fail)
	if err := logging.Setup(logging.Options{Console: GinkgoWriter}); err != nil {
		t.Fatal(err)
	}
	RunSpecs(t, "Handlers Suite")
}

// rt is the state of the agent app the specs run, made again before each spec so no spec depends on what another one
// left
var rt *runtime.Runtime

var _ = BeforeEach(func() {
	rt = runtime.New()
	ipvsctl.SetClient(ipvsctl.NewFakeIpvsClient())
})

func makeVip(uuid, ip string, typeAnnounce starling_announce.StarlingAnnounce_TypeAnnounce, vsUuids ...string) *vip.VIP {
	announce, err := anypb.New(&starling_announce.StarlingAnnounce{TypeAnnounce: typeAnnounce})
	Expect(err).ToNot(HaveOccurred())
	return &vip.VIP{
		UUID:               uuid,
		Name:               uuid,
		Ip:                 ip,
		VirtualServersUuid: vsUuids,
		TypedAnnounce:      announce,
	}
}

func ipNet(cidr string) *net.IPNet {
	_, n, err := net.ParseCIDR(cidr)
	Expect(err).ToNot(HaveOccurred())
	return n
}

func makeBackend(uuid string) *backend.Backend {
	return &backend.Backend{
		UUID:           uuid,
		Name:           uuid,
		ConnectTimeout: durationpb.New(5 * time.Second),
		LbPolicy:       backend.Backend_ROUND_ROBIN,
	}
}

// envoyStub acks each response by opening a new watch, as envoy does
type envoyStub struct {
	mutex     sync.Mutex
	responses []*discovery.DeltaDiscoveryResponse
}

func startEnvoyStub(ctx context.Context, cw *envoyctl.ConfigWatcher, typeUrl string) *envoyStub {
	stub := &envoyStub{}
	go func() {
		defer GinkgoRecover()
		for {
			respChan := make(chan cache.DeltaResponse, 1)
			cancel, err := cw.CreateDeltaWatch(&cache.DeltaRequest{TypeUrl: typeUrl}, stream.NewDeltaSubscription(nil, nil, nil, true), respChan)
			Expect(err).ToNot(HaveOccurred())
			select {
			case <-ctx.Done():
				cancel()
				return
			case resp := <-respChan:
				deltaResp, err := resp.GetDeltaDiscoveryResponse()
				Expect(err).ToNot(HaveOccurred())
				stub.mutex.Lock()
				stub.responses = append(stub.responses, deltaResp)
				stub.mutex.Unlock()
			}
		}
	}()
	return stub
}

// ResourceNames gives names of resources sent, prefixed by '-' when it was a removal
func (s *envoyStub) ResourceNames() []string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	names := make([]string, 0)
	for _, resp := range s.responses {
		for _, res := range resp.GetResources() {
			names = append(names, res.GetName())
		}
		for _, removed := range resp.GetRemovedResources() {
			names = append(names, "-"+removed)
		}
	}
	return names
}

func (s *envoyStub) LastResource(message proto.Message) {
	GinkgoHelper()
	s.mutex.Lock()
	defer s.mutex.Unlock()
	Expect(s.responses).ToNot(BeEmpty())
	resources := s.responses[len(s.responses)-1].GetResources()
	Expect(resources).To(HaveLen(1))
	Expect(resources[0].GetResource().UnmarshalTo(message)).To(Succeed())
}

func itoa(i int) string {
	return strconv.Itoa(i)
}
