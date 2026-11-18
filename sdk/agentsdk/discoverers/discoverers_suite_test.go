package discoverers_test

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/miekg/dns"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"

	. "github.com/seagalia/flock/sdk/agentsdk/discoverers"
)

func TestDiscoverers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Discoverers Suite")
}

// runDiscovery runs disc until the end of the spec, which waits for it: a discovery left running would report its end
// in a later spec. A stop ends it with no error or with the error of its ctx.
func runDiscovery(disc Discoverer) <-chan []*core.IPAddr {
	found := make(chan []*core.IPAddr)
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan error, 1)
	go func() { ended <- disc.Run(ctx, found) }()
	DeferCleanup(func() {
		cancel()
		Eventually(ended).Should(Receive(Or(BeNil(), MatchError(context.Canceled))))
	})
	return found
}

type DNSServer struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	addr       net.Addr
	handler    dns.Handler
	mutex      *sync.Mutex
}

func NewDNSServer(handler dns.Handler) *DNSServer {
	ctx, cancelFunc := context.WithCancel(context.Background())
	mutex := &sync.Mutex{}
	return &DNSServer{
		ctx:        ctx,
		cancelFunc: cancelFunc,
		mutex:      mutex,
		handler: dns.HandlerFunc(func(writer dns.ResponseWriter, msg *dns.Msg) {
			mutex.Lock()
			defer mutex.Unlock()
			handler.ServeDNS(writer, msg)
		}),
	}
}

func runDnsServer(srv *dns.Server, notifyStartedFunc func()) {
	defer GinkgoRecover()
	srv.NotifyStartedFunc = notifyStartedFunc
	if err := srv.ListenAndServe(); err != nil {
		Expect(err).ToNot(HaveOccurred())
	}
}

func (s *DNSServer) LockHandler() {
	s.mutex.Lock()
}

func (s *DNSServer) UnlockHandler() {
	s.mutex.Unlock()
}

func (s *DNSServer) Stop() {
	s.cancelFunc()
}

func (s *DNSServer) IpAddr() *core.IPAddr {
	host, port, err := net.SplitHostPort(s.addr.String())
	Expect(err).ToNot(HaveOccurred())
	portInt, err := strconv.Atoi(port)
	Expect(err).ToNot(HaveOccurred())
	return &core.IPAddr{
		Ip:   host,
		Port: uint32(portInt),
	}
}

func (s *DNSServer) Run(ready chan<- struct{}) {
	entry := log.Logger.With().Str("server", "dns").Logger()
	udpServer := &dns.Server{
		Net:     "udp",
		Handler: s.handler,
	}
	tcpServer := &dns.Server{
		Addr:    "127.0.0.1:0",
		Net:     "tcp",
		Handler: s.handler,
	}

	readyTcp := make(chan struct{})

	go runDnsServer(tcpServer, func() {
		readyTcp <- struct{}{}
	})
	<-readyTcp
	close(readyTcp)
	if tcpServer.Listener != nil {
		s.addr = tcpServer.Listener.Addr()
		udpServer.Addr = tcpServer.Listener.Addr().String()
	}
	go runDnsServer(udpServer, func() {
		ready <- struct{}{}
	})
	entry.Info().Msgf("starting udp and tcp dns server on %s", tcpServer.Listener.Addr().String())
	<-s.ctx.Done()
	err := udpServer.Shutdown()
	Expect(err).ToNot(HaveOccurred())
	err = tcpServer.Shutdown()
	Expect(err).ToNot(HaveOccurred())
}

type EasyHandler struct {
	fqdn   string
	allRrs []dns.RR
	strict bool
}

func NewEasyHandler(domain string, ipAddresses []*core.IPAddr, strict bool) *EasyHandler {
	allRrs := make([]dns.RR, 0, len(ipAddresses))
	fqdn := dns.Fqdn(domain)
	for _, ipAddr := range ipAddresses {
		var queryTypeStr string
		if ipAddr.IsIPV6() {
			queryTypeStr = "AAAA"
		} else {
			queryTypeStr = "A"
		}
		rr, err := dns.NewRR(fmt.Sprintf("%s 30 IN %s %s", fqdn, queryTypeStr, ipAddr.Ip))
		Expect(err).ToNot(HaveOccurred())
		allRrs = append(allRrs, rr)
	}
	return &EasyHandler{
		fqdn:   fqdn,
		allRrs: allRrs,
		strict: strict,
	}
}

func (h *EasyHandler) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	m := new(dns.Msg)
	m.SetReply(r)
	rrs := make([]dns.RR, 0)
	for _, q := range r.Question {
		if dns.Fqdn(q.Name) != h.fqdn {
			continue
		}
		for _, rr := range h.allRrs {
			if rr.Header().Rrtype == q.Qtype {
				rrs = append(rrs, rr)
			}
		}
	}
	if len(rrs) == 0 && h.strict {
		m.SetRcode(r, dns.RcodeNameError)
	} else {
		m.Answer = rrs
	}

	err := w.WriteMsg(m)
	if err != nil {
		Expect(err).ToNot(HaveOccurred())
	}
}
