package nm

import (
	"context"
	"errors"
	"net"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/knftables"

	"github.com/seagalia/flock/agent/starling/config"
)

var defaultNmConf = config.NetworkManagerConfig{
	SnatMark:           15181,
	SnatLogMark:        15182,
	LogOnlyMark:        15183,
	DummyInterfaceName: "flock0",
}

func dumpLines(fake *knftables.Fake) []string {
	return strings.Split(strings.TrimSpace(fake.Dump()), "\n")
}

var _ = Describe("nftables table", func() {
	var (
		ctx  context.Context
		fake *knftables.Fake
	)

	BeforeEach(func() {
		ctx = context.Background()
		fake = knftables.NewFake(knftables.InetFamily, nftTable)
	})

	initRules := []string{
		"add rule inet flock prerouting ct state invalid drop",
		"add rule inet flock prerouting tcp flags & (fin|syn|rst|ack) != syn ct state new drop",
		"add rule inet flock prerouting ct state new tcp option maxseg size != 536-65535 drop",
		"add rule inet flock prerouting tcp flags & (fin|syn) == fin|syn drop",
		"add rule inet flock prerouting tcp flags & (syn|rst) == syn|rst drop",
		"add rule inet flock prerouting tcp flags & (fin|rst) == fin|rst drop",
		"add rule inet flock prerouting tcp flags & (fin|ack) == fin drop",
		"add rule inet flock prerouting tcp flags & (ack|urg) == urg drop",
		"add rule inet flock prerouting tcp flags & (ack|psh) == psh drop",
		"add rule inet flock prerouting tcp flags & (fin|syn|rst|psh|ack|urg) == 0x0 drop",
		"add rule inet flock input ct state established,related accept",
		"add rule inet flock output ct state established,related accept",
	}

	It("hooks its base chains before the iptables ones", func() {
		Expect(newNftRules(fake, defaultNmConf, nil).setup(ctx)).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElements(
			"add chain inet flock prerouting { type filter hook prerouting priority mangle - 5 ; policy accept ; }",
			"add chain inet flock input { type filter hook input priority filter - 10 ; policy accept ; }",
			"add chain inet flock output { type filter hook output priority filter - 10 ; policy accept ; }",
			"add chain inet flock postrouting { type nat hook postrouting priority srcnat - 10 ; policy accept ; }",
			"add map inet flock vs_prerouting4 { type inet_proto . ipv4_addr . inet_service : verdict ; flags interval ; }",
			"add map inet flock vs_prerouting6 { type inet_proto . ipv6_addr . inet_service : verdict ; flags interval ; }",
			"add map inet flock vs_input4 { type inet_proto . ipv4_addr . inet_service : verdict ; flags interval ; }",
			"add map inet flock vs_input6 { type inet_proto . ipv6_addr . inet_service : verdict ; flags interval ; }",
		))
	})

	It("dispatches v4 and v6 through the vs maps", func() {
		Expect(newNftRules(fake, defaultNmConf, nil).setup(ctx)).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElements(
			"add rule inet flock prerouting meta l4proto . ip daddr . th dport vmap @vs_prerouting4",
			"add rule inet flock prerouting meta l4proto . ip6 daddr . th dport vmap @vs_prerouting6",
			"add rule inet flock input meta l4proto . ip daddr . th dport vmap @vs_input4",
			"add rule inet flock input meta l4proto . ip6 daddr . th dport vmap @vs_input6",
		))
	})

	It("adds the init rules", func() {
		Expect(newNftRules(fake, defaultNmConf, nil).setup(ctx)).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElements(initRules))
	})

	It("skips the init rules when they are disabled", func() {
		conf := defaultNmConf
		conf.DisableInitRules = true
		Expect(newNftRules(fake, conf, nil).setup(ctx)).To(Succeed())
		lines := dumpLines(fake)
		for _, rule := range initRules {
			Expect(lines).ToNot(ContainElement(rule))
		}
		Expect(lines).To(ContainElement("add rule inet flock input meta l4proto . ip daddr . th dport vmap @vs_input4"))
	})

	It("logs marked output packets", func() {
		conf := defaultNmConf
		conf.DisableInitRules = true
		Expect(newNftRules(fake, conf, nil).setup(ctx)).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElements(
			"add rule inet flock output meta mark { 15182, 15183 } tcp flags & (fin|syn|rst|ack) == syn log group 15182",
			"add rule inet flock output meta mark { 15182, 15183 } meta l4proto udp log group 15182",
		))
	})

	It("is idempotent", func() {
		rules := newNftRules(fake, defaultNmConf, nil)
		Expect(rules.setup(ctx)).To(Succeed())
		firstDump := fake.Dump()
		Expect(rules.setup(ctx)).To(Succeed())
		Expect(fake.Dump()).To(Equal(firstDump))
	})

	It("snats marked packets per interface, the table not set up yet", func() {
		sources := []snatSource{
			{iface: "eth0", ip: net.ParseIP("172.31.250.2")},
			{iface: "eth1", ip: net.ParseIP("fd00::2")},
		}
		Expect(addSnatSources(ctx, fake, defaultNmConf, sources)).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElements(
			"add chain inet flock postrouting { type nat hook postrouting priority srcnat - 10 ; policy accept ; }",
			`add rule inet flock postrouting oifname "eth0" meta mark { 15181, 15182 } snat ip to 172.31.250.2`,
			`add rule inet flock postrouting oifname "eth1" meta mark { 15181, 15182 } snat ip6 to fd00::2`,
		))
	})

	It("keeps the snat rules when the table is set up after them", func() {
		sources := []snatSource{{iface: "eth0", ip: net.ParseIP("172.31.250.2")}}
		Expect(addSnatSources(ctx, fake, defaultNmConf, sources)).To(Succeed())
		Expect(newNftRules(fake, defaultNmConf, nil).setup(ctx)).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElement(
			`add rule inet flock postrouting oifname "eth0" meta mark { 15181, 15182 } snat ip to 172.31.250.2`,
		))
	})

	It("deletes its table, existing or not", func() {
		Expect(deleteTable(ctx, fake)).To(Succeed())
		Expect(newNftRules(fake, defaultNmConf, nil).setup(ctx)).To(Succeed())
		Expect(deleteTable(ctx, fake)).To(Succeed())
		Expect(strings.TrimSpace(fake.Dump())).To(BeEmpty())
	})
})

// failingOnceNft fails its first transaction, like nft refusing a change
type failingOnceNft struct {
	*knftables.Fake
	failed bool
}

func (f *failingOnceNft) Run(ctx context.Context, tx *knftables.Transaction) error {
	if !f.failed {
		f.failed = true
		return errors.New("nft failed")
	}
	return f.Fake.Run(ctx, tx)
}

var _ = Describe("virtual server rules", func() {
	const vsChain = "vs_10_250_0_70_32_80_tcp"
	var (
		ctx       context.Context
		fake      *knftables.Fake
		rules     *nftRules
		setupDump string
		tcpKey    vsKey
	)

	cidr := func(raw string) *net.IPNet {
		_, ipNet, err := net.ParseCIDR(raw)
		Expect(err).ToNot(HaveOccurred())
		return ipNet
	}

	BeforeEach(func() {
		ctx = context.Background()
		fake = knftables.NewFake(knftables.InetFamily, nftTable)
		rules = newNftRules(fake, defaultNmConf, nil)
		Expect(rules.setup(ctx)).To(Succeed())
		setupDump = fake.Dump()
		tcpKey = newVsKey(cidr("10.250.0.70/32"), 80, false)
	})

	It("names a vs after its vip, port and protocol", func() {
		Expect(vsName(tcpKey)).To(Equal(vsChain))
		Expect(vsName(newVsKey(cidr("2001:db8::1/128"), 53, true))).To(Equal("vs_2001_db8__1_128_53_udp"))
	})

	It("marks packets of a vs", func() {
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.mark = 15181 })).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElements(
			"add chain inet flock "+vsChain+"_pre",
			"add rule inet flock "+vsChain+"_pre meta mark set 15181",
			"add element inet flock vs_prerouting4 { tcp . 10.250.0.70/32 . 80 : jump "+vsChain+"_pre }",
		))
	})

	It("logs new tcp connections of a vs", func() {
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.nflogPrefix = "vs-lim" })).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElements(
			"add rule inet flock "+vsChain+`_in tcp flags & (fin|syn|rst|ack) == syn log prefix "vs-lim" group 15182`,
			"add element inet flock vs_input4 { tcp . 10.250.0.70/32 . 80 : jump "+vsChain+"_in }",
		))
	})

	It("logs every udp packet of a vs", func() {
		udpKey := newVsKey(cidr("10.250.0.70/32"), 53, true)
		Expect(rules.updateVs(ctx, udpKey, func(v *vsRules) { v.nflogPrefix = "vs-dns" })).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElement(
			`add rule inet flock vs_10_250_0_70_32_53_udp_in log prefix "vs-dns" group 15182`,
		))
	})

	DescribeTable("conn limits",
		func(limit connLimit, expected []string) {
			Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.connLimit = &limit })).To(Succeed())
			Expect(dumpLines(fake)).To(ContainElements(expected))
		},
		Entry("for the whole vs", connLimit{max: 10}, []string{
			"add rule inet flock " + vsChain + "_in ct state new ct count over 10 drop",
		}),
		Entry("per source", connLimit{max: 10, bySource: true}, []string{
			"add set inet flock " + vsChain + "_conn_src { type ipv4_addr ; flags dynamic ; }",
			"add rule inet flock " + vsChain + "_in ct state new update @" + vsChain + "_conn_src { ip saddr ct count over 10 } drop",
		}),
		Entry("per destination", connLimit{max: 10, byDestination: true}, []string{
			"add set inet flock " + vsChain + "_conn_dst { type ipv4_addr ; flags dynamic ; }",
			"add rule inet flock " + vsChain + "_in ct state new update @" + vsChain + "_conn_dst { ip daddr ct count over 10 } drop",
		}),
		Entry("per source and per destination", connLimit{max: 10, bySource: true, byDestination: true}, []string{
			"add rule inet flock " + vsChain + "_in ct state new update @" + vsChain + "_conn_src { ip saddr ct count over 10 } drop",
			"add rule inet flock " + vsChain + "_in ct state new update @" + vsChain + "_conn_dst { ip daddr ct count over 10 } drop",
		}),
	)

	DescribeTable("rate limits",
		func(isUdp bool, limit rateLimit, expected []string) {
			key := newVsKey(cidr("10.250.0.70/32"), 80, isUdp)
			Expect(rules.updateVs(ctx, key, func(v *vsRules) { v.rateLimit = &limit })).To(Succeed())
			Expect(dumpLines(fake)).To(ContainElements(expected))
		},
		Entry("for the whole tcp vs", false, rateLimit{rate: "100/second", burst: 20}, []string{
			"add rule inet flock " + vsChain + "_in ct state new limit rate over 100/second burst 20 packets drop",
		}),
		Entry("on every udp packet", true, rateLimit{rate: "100/second", burst: 20}, []string{
			"add rule inet flock vs_10_250_0_70_32_80_udp_in limit rate over 100/second burst 20 packets drop",
		}),
		Entry("without burst", false, rateLimit{rate: "100/second"}, []string{
			"add rule inet flock " + vsChain + "_in ct state new limit rate over 100/second drop",
		}),
		Entry("per source", false, rateLimit{rate: "100/second", burst: 20, bySource: true}, []string{
			"add set inet flock " + vsChain + "_rate_src { type ipv4_addr ; flags dynamic ; timeout 60s ; }",
			"add rule inet flock " + vsChain + "_in ct state new update @" + vsChain + "_rate_src { ip saddr limit rate over 100/second burst 20 packets } drop",
		}),
		Entry("per destination", false, rateLimit{rate: "100/second", burst: 20, byDestination: true}, []string{
			"add set inet flock " + vsChain + "_rate_dst { type ipv4_addr ; flags dynamic ; timeout 60s ; }",
			"add rule inet flock " + vsChain + "_in ct state new update @" + vsChain + "_rate_dst { ip daddr limit rate over 100/second burst 20 packets } drop",
		}),
		Entry("per source and destination pair", false, rateLimit{rate: "100/second", burst: 20, bySource: true, byDestination: true}, []string{
			"add set inet flock " + vsChain + "_rate_srcdst { type ipv4_addr . ipv4_addr ; flags dynamic ; timeout 60s ; }",
			"add rule inet flock " + vsChain + "_in ct state new update @" + vsChain + "_rate_srcdst { ip saddr . ip daddr limit rate over 100/second burst 20 packets } drop",
		}),
	)

	It("puts the nflog rule before the limits", func() {
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) {
			v.rateLimit = &rateLimit{rate: "100/second"}
			v.connLimit = &connLimit{max: 10}
			v.nflogPrefix = "vs-lim"
		})).To(Succeed())
		var vsRuleLines []string
		for _, line := range dumpLines(fake) {
			if strings.HasPrefix(line, "add rule inet flock "+vsChain+"_in ") {
				vsRuleLines = append(vsRuleLines, line)
			}
		}
		Expect(vsRuleLines).To(HaveLen(3))
		Expect(vsRuleLines[0]).To(ContainSubstring("log prefix"))
		Expect(vsRuleLines[1]).To(ContainSubstring("ct count"))
		Expect(vsRuleLines[2]).To(ContainSubstring("limit rate"))
	})

	It("uses ip6 and the v6 maps for an ipv6 vip", func() {
		key := newVsKey(cidr("2001:db8::1/128"), 53, true)
		Expect(rules.updateVs(ctx, key, func(v *vsRules) {
			v.connLimit = &connLimit{max: 5, bySource: true}
			v.mark = 15181
		})).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElements(
			"add set inet flock vs_2001_db8__1_128_53_udp_conn_src { type ipv6_addr ; flags dynamic ; }",
			"add rule inet flock vs_2001_db8__1_128_53_udp_in ct state new update @vs_2001_db8__1_128_53_udp_conn_src { ip6 saddr ct count over 5 } drop",
			"add element inet flock vs_input6 { udp . 2001:db8::1/128 . 53 : jump vs_2001_db8__1_128_53_udp_in }",
			"add element inet flock vs_prerouting6 { udp . 2001:db8::1/128 . 53 : jump vs_2001_db8__1_128_53_udp_pre }",
		))
	})

	It("keys a prefix vip by its cidr", func() {
		key := newVsKey(cidr("10.250.0.0/24"), 80, false)
		Expect(rules.updateVs(ctx, key, func(v *vsRules) { v.mark = 15181 })).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElement(
			"add element inet flock vs_prerouting4 { tcp . 10.250.0.0/24 . 80 : jump vs_10_250_0_0_24_80_tcp_pre }",
		))
	})

	It("keeps the counters of a limit when another part of the vs changes", func() {
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.connLimit = &connLimit{max: 10, bySource: true} })).To(Succeed())
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.nflogPrefix = "vs-lim" })).To(Succeed())
		Expect(fake.LastTransaction.String()).ToNot(ContainSubstring("delete set"))
		Expect(dumpLines(fake)).To(ContainElement("add set inet flock " + vsChain + "_conn_src { type ipv4_addr ; flags dynamic ; }"))
	})

	It("deletes the sets a vs doesn't use anymore", func() {
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) {
			v.connLimit = &connLimit{max: 10, bySource: true}
			v.nflogPrefix = "vs-lim"
		})).To(Succeed())
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.connLimit = nil })).To(Succeed())
		Expect(fake.Dump()).ToNot(ContainSubstring("_conn_src"))
		Expect(fake.Dump()).To(ContainSubstring(`log prefix "vs-lim"`))
	})

	It("removes every object of a vs when it has nothing left", func() {
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) {
			v.mark = 15182
			v.nflogPrefix = "vs-lim"
			v.connLimit = &connLimit{max: 10, bySource: true}
			v.rateLimit = &rateLimit{rate: "100/second", byDestination: true}
		})).To(Succeed())
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { *v = vsRules{} })).To(Succeed())
		Expect(fake.Dump()).To(Equal(setupDump))
	})

	It("keeps the snat mark when the log-only mark is cleared", func() {
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.mark = defaultNmConf.SnatMark })).To(Succeed())
		Expect(rules.updateVs(ctx, tcpKey, clearLogOnlyMark(defaultNmConf))).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElement("add rule inet flock " + vsChain + "_pre meta mark set 15181"))
	})

	It("keeps the log-only mark when the snat mark is cleared", func() {
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.mark = defaultNmConf.LogOnlyMark })).To(Succeed())
		Expect(rules.updateVs(ctx, tcpKey, clearSnatMark(defaultNmConf))).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElement("add rule inet flock " + vsChain + "_pre meta mark set 15183"))
	})

	It("runs no transaction when a change keeps the rules", func() {
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) {
			v.mark = defaultNmConf.SnatMark
			v.connLimit = &connLimit{max: 10, bySource: true}
		})).To(Succeed())
		lastApplied := fake.LastTransaction
		Expect(rules.updateVs(ctx, tcpKey, clearLogOnlyMark(defaultNmConf))).To(Succeed())
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.nflogPrefix = "" })).To(Succeed())
		Expect(fake.LastTransaction).To(BeIdenticalTo(lastApplied))
	})

	It("clears the marks it owns", func() {
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.mark = defaultNmConf.SnatLogMark })).To(Succeed())
		Expect(rules.updateVs(ctx, tcpKey, clearSnatMark(defaultNmConf))).To(Succeed())
		Expect(rules.updateVs(ctx, newVsKey(cidr("10.250.0.71/32"), 80, false), func(v *vsRules) { v.mark = defaultNmConf.LogOnlyMark })).To(Succeed())
		Expect(rules.updateVs(ctx, newVsKey(cidr("10.250.0.71/32"), 80, false), clearLogOnlyMark(defaultNmConf))).To(Succeed())
		Expect(fake.Dump()).To(Equal(setupDump))
	})

	It("keeps its model when nft fails, so the next change applies it again", func() {
		failing := &failingOnceNft{Fake: fake}
		rules = newNftRules(failing, defaultNmConf, nil)
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.mark = 15181 })).ToNot(Succeed())
		Expect(fake.Dump()).To(Equal(setupDump))
		Expect(rules.updateVs(ctx, tcpKey, func(v *vsRules) { v.mark = 15181 })).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElement("add rule inet flock " + vsChain + "_pre meta mark set 15181"))
	})
})
