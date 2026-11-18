package agentlist_test

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/seagalia/flock/agent/starling/agentlist"
)

func TestAgentlist(t *testing.T) {
	RegisterFailHandler(Fail)
	if err := logging.Setup(logging.Options{Console: GinkgoWriter}); err != nil {
		t.Fatal(err)
	}
	RunSpecs(t, "Agentlist Suite")
}

// vips is the store of the placers, shared by the placers of a spec as the agents share the vips of their instance
var vips storers.Storer[*vip.VIP]

var _ = BeforeEach(func() {
	vips = storers.NewMemoryManager().VIPs()
})

func makeVip(uuid, ip string, typeAnnounce starling_announce.StarlingAnnounce_TypeAnnounce) *vip.VIP {
	announce, err := anypb.New(&starling_announce.StarlingAnnounce{TypeAnnounce: typeAnnounce})
	Expect(err).ToNot(HaveOccurred())
	return &vip.VIP{
		UUID:          uuid,
		Name:          uuid,
		Ip:            ip,
		TypedAnnounce: announce,
	}
}

func vipsIps(vips []*vip.VIP) []string {
	ips := make([]string, len(vips))
	for i, aVip := range vips {
		ips[i] = aVip.GetIp()
	}
	return ips
}

// freePort is free for both tcp and udp
func freePort() uint16 {
	for {
		tcpListener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		port := tcpListener.Addr().(*net.TCPAddr).Port
		udpConn, err := net.ListenPacket("udp", fmt.Sprintf("127.0.0.1:%d", port))
		tcpListener.Close()
		if err != nil {
			continue
		}
		udpConn.Close()
		return uint16(port)
	}
}

type recordingPlacerListener struct {
	mutex          sync.Mutex
	added          []*vip.VIP
	removed        []*vip.VIP
	afterCalculate [][]*vip.VIP
	errOnAdd       error
	errOnRemove    error
}

func (l *recordingPlacerListener) OnPlacerAddedVip(_ context.Context, aVip *vip.VIP) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.added = append(l.added, aVip)
	return l.errOnAdd
}

func (l *recordingPlacerListener) OnPlacerRemoveVip(_ context.Context, aVip *vip.VIP) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.removed = append(l.removed, aVip)
	return l.errOnRemove
}

func (l *recordingPlacerListener) OnAfterCalculate(_ context.Context, vips []*vip.VIP) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.afterCalculate = append(l.afterCalculate, vips)
	return nil
}

func (l *recordingPlacerListener) Added() []string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return vipsIps(l.added)
}

func (l *recordingPlacerListener) Removed() []string {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return vipsIps(l.removed)
}

func (l *recordingPlacerListener) NbAfterCalculate() int {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	return len(l.afterCalculate)
}

// startAgentList runs an agent list on 127.0.0.1:port joining the one on peerPort, until the spec ends or the
// returned stop is called, which gives the error of Run
func startAgentList(name string, port, peerPort uint16) (*agentlist.AgentList, func() error) {
	GinkgoHelper()
	list, err := agentlist.New(name, "127.0.0.1", port, "a-shared-secret-of-16b", []string{fmt.Sprintf("127.0.0.1:%d", peerPort)})
	Expect(err).ToNot(HaveOccurred())
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- list.Run(ctx)
	}()
	stopped := false
	var stopErr error
	stop := func() error {
		if stopped {
			return stopErr
		}
		stopped = true
		cancel()
		stopErr = <-runErr
		return stopErr
	}
	DeferCleanup(stop)
	return list, stop
}
