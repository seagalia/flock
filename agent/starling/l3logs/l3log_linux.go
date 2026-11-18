//go:build linux

package l3logs

import (
	"context"
	"fmt"
	"net"

	"github.com/florianl/go-nflog/v2"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/agentsdk/stores"
	"github.com/seagalia/flock/sdk/logging"
)

type L3LoggerLinux struct {
	nflogConf *nflog.Config
	entry     zerolog.Logger
	ipPorts   *stores.IpPortTranslator
}

// NewL3Logger names in the logs the virtual server ipPorts finds for each packet.
func NewL3Logger(ipPorts *stores.IpPortTranslator) L3Logger {
	return &L3LoggerLinux{
		ipPorts: ipPorts,
		nflogConf: &nflog.Config{
			Group:    DefaultNflogGroup,
			Copymode: nflog.CopyPacket,
		},
		entry: logging.Component("l3logger"),
	}
}

func (l *L3LoggerLinux) Run(ctx context.Context, afterInit func()) error {
	nf, err := nflog.Open(l.nflogConf)
	if err != nil {
		return fmt.Errorf("could not open nflog socket: %w", err)
	}
	defer nf.Close()

	errFunc := func(e error) int {
		l.entry.Error().Msgf("nflog error: %s", e.Error())
		return 0
	}

	err = nf.RegisterWithErrorFunc(ctx, l.hookLog, errFunc)
	if err != nil {
		return fmt.Errorf("could not register nflog callback: %w", err)
	}

	if afterInit != nil {
		afterInit()
	}

	<-ctx.Done()
	return nil
}

func (l *L3LoggerLinux) hookLog(attrs nflog.Attribute) int {
	entry := l.entry.With().Bool("l3log", true).Logger()
	statToUse := stats.incomingPackets
	statLabels := make(prometheus.Labels)
	logMessage := "incoming packet"
	vsUuid := ""
	if attrs.Prefix != nil && *attrs.Prefix != "" {
		entry = entry.With().Str("filter", "input").Logger()
		vsUuid = *attrs.Prefix
	} else {
		entry = entry.With().Str("filter", "output").Logger()
		logMessage = "outgoing packet"
		statToUse = stats.outgoingPackets
	}

	var payload []byte
	if attrs.Payload != nil {
		payload = *attrs.Payload
	}
	var srcIp, dstIp net.IP
	var srcPort, dstPort uint16

	statLabels["ip_format"] = "ipv4"
	firstLayer := layers.LayerTypeIPv4
	if len(payload) > 0 && payload[0]>>4 == 6 {
		statLabels["ip_format"] = "ipv6"
		firstLayer = layers.LayerTypeIPv6
	}
	packet := gopacket.NewPacket(payload, firstLayer, gopacket.NoCopy)
	if ipv4 := packet.Layer(layers.LayerTypeIPv4); ipv4 != nil {
		ipInfo, _ := ipv4.(*layers.IPv4)
		srcIp = ipInfo.SrcIP
		dstIp = ipInfo.DstIP
	}
	if ipv6 := packet.Layer(layers.LayerTypeIPv6); ipv6 != nil {
		ipInfo6, _ := ipv6.(*layers.IPv6)
		srcIp = ipInfo6.SrcIP
		dstIp = ipInfo6.DstIP
	}
	entry = entry.With().Stringer("ip_src", srcIp).Stringer("ip_dst", dstIp).Logger()
	protocol := "unknown"
	if tcpLayer := packet.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		tcp, _ := tcpLayer.(*layers.TCP)
		protocol = "tcp"
		srcPort = uint16(tcp.SrcPort)
		dstPort = uint16(tcp.DstPort)
	}
	if udpLayer := packet.Layer(layers.LayerTypeUDP); udpLayer != nil {
		udp, _ := udpLayer.(*layers.UDP)
		protocol = "udp"
		srcPort = uint16(udp.SrcPort)
		dstPort = uint16(udp.DstPort)
	}

	if vsUuid == "" {
		vsUuid = l.findVsUuid(srcIp.String(), uint32(dstPort), protocol)
	}
	if vsUuid == "" {
		vsUuid = l.findVsUuid(dstIp.String(), uint32(dstPort), protocol)
	}
	if vsUuid == "" {
		vsUuid = "unknown"
	}
	statLabels["source_ip"] = srcIp.String()
	statLabels["destination_ip"] = dstIp.String()
	statLabels["protocol"] = protocol
	statLabels["vs_uuid"] = vsUuid
	statLabels["destination_port"] = fmt.Sprintf("%d", dstPort)

	statToUse.With(statLabels).Inc()
	entry.Info().
		Str("vs_uuid", vsUuid).
		Uint16("src_port", srcPort).
		Uint16("dst_port", dstPort).
		Str("protocol", protocol).
		Msg(fmt.Sprint(logMessage))
	return 0
}

func (l *L3LoggerLinux) findVsUuid(ip string, port uint32, protocol string) string {
	virtServ, ok := l.ipPorts.FindVs(ip, port, protocol)
	if ok {
		return virtServ.GetUUID()
	}
	return ""
}
