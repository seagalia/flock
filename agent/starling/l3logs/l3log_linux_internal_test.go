//go:build linux

package l3logs

import (
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/storers"
	"net"

	"github.com/florianl/go-nflog/v2"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func tcpPacket(network gopacket.NetworkLayer, srcPort, dstPort uint16) []byte {
	GinkgoHelper()
	tcp := &layers.TCP{SrcPort: layers.TCPPort(srcPort), DstPort: layers.TCPPort(dstPort), SYN: true}
	Expect(tcp.SetNetworkLayerForChecksum(network)).To(Succeed())
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	Expect(gopacket.SerializeLayers(buf, opts, network.(gopacket.SerializableLayer), tcp)).To(Succeed())
	return buf.Bytes()
}

func udpPacket(src, dst string, srcPort, dstPort uint16) []byte {
	GinkgoHelper()
	ip := &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolUDP, SrcIP: net.ParseIP(src), DstIP: net.ParseIP(dst)}
	udp := &layers.UDP{SrcPort: layers.UDPPort(srcPort), DstPort: layers.UDPPort(dstPort)}
	Expect(udp.SetNetworkLayerForChecksum(ip)).To(Succeed())
	buf := gopacket.NewSerializeBuffer()
	opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
	Expect(gopacket.SerializeLayers(buf, opts, ip, udp)).To(Succeed())
	return buf.Bytes()
}

var _ = Describe("L3LoggerLinux hookLog", func() {
	var logger *L3LoggerLinux

	BeforeEach(func() {
		logger = NewL3Logger(stores.NewIpPortTranslator(storers.NewMemoryManager())).(*L3LoggerLinux)
	})

	count := func(counter *prometheus.CounterVec, labels prometheus.Labels) float64 {
		return testutil.ToFloat64(counter.With(labels))
	}

	It("should count incoming ipv4 tcp packet for the virtual server given as prefix", func() {
		ip := &layers.IPv4{Version: 4, TTL: 64, Protocol: layers.IPProtocolTCP, SrcIP: net.ParseIP("192.168.0.1"), DstIP: net.ParseIP("10.0.0.1")}
		payload := tcpPacket(ip, 40000, 443)
		prefix := "vs-uuid-1"
		labels := prometheus.Labels{
			"source_ip": "192.168.0.1", "destination_ip": "10.0.0.1", "destination_port": "443",
			"protocol": "tcp", "ip_format": "ipv4", "vs_uuid": "vs-uuid-1",
		}
		before := count(stats.incomingPackets, labels)

		Expect(logger.hookLog(nflog.Attribute{Payload: &payload, Prefix: &prefix})).To(Equal(0))
		Expect(count(stats.incomingPackets, labels)).To(Equal(before + 1))
	})

	It("should count incoming ipv6 tcp packet", func() {
		ip := &layers.IPv6{Version: 6, HopLimit: 64, NextHeader: layers.IPProtocolTCP, SrcIP: net.ParseIP("2001:db8::2"), DstIP: net.ParseIP("2001:db8::1")}
		payload := tcpPacket(ip, 40000, 80)
		prefix := "vs-uuid-6"
		labels := prometheus.Labels{
			"source_ip": "2001:db8::2", "destination_ip": "2001:db8::1", "destination_port": "80",
			"protocol": "tcp", "ip_format": "ipv6", "vs_uuid": "vs-uuid-6",
		}
		before := count(stats.incomingPackets, labels)

		Expect(logger.hookLog(nflog.Attribute{Payload: &payload, Prefix: &prefix})).To(Equal(0))
		Expect(count(stats.incomingPackets, labels)).To(Equal(before + 1))
	})

	It("should count outgoing udp packet of unknown virtual server", func() {
		payload := udpPacket("10.0.0.1", "192.168.0.1", 53, 40000)
		labels := prometheus.Labels{
			"source_ip": "10.0.0.1", "destination_ip": "192.168.0.1", "destination_port": "40000",
			"protocol": "udp", "ip_format": "ipv4", "vs_uuid": "unknown",
		}
		before := count(stats.outgoingPackets, labels)

		Expect(logger.hookLog(nflog.Attribute{Payload: &payload})).To(Equal(0))
		Expect(count(stats.outgoingPackets, labels)).To(Equal(before + 1))
	})

	It("should not panic on packet without payload", func() {
		Expect(logger.hookLog(nflog.Attribute{})).To(Equal(0))
	})
})
