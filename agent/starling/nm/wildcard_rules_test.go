package nm

import (
	"context"
	"net"
	"sync"
	"syscall"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/knftables"
)

type fakeMachineIPs struct {
	mutex sync.Mutex
	ipv4  []net.IP
	ipv6  []net.IP
}

func (f *fakeMachineIPs) Current(family int) []net.IP {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	if family == syscall.AF_INET6 {
		return f.ipv6
	}
	return f.ipv4
}

func (f *fakeMachineIPs) Set(ips ...string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.ipv4, f.ipv6 = nil, nil
	for _, raw := range ips {
		ip := net.ParseIP(raw)
		if ip.To4() != nil {
			f.ipv4 = append(f.ipv4, ip.To4())
			continue
		}
		f.ipv6 = append(f.ipv6, ip)
	}
}

var _ = Describe("wildcard vip rules", func() {
	var (
		ctx       context.Context
		fake      *knftables.Fake
		addresses *fakeMachineIPs
		rules     *nftRules
		wildcard  vsKey
		concrete  vsKey
	)

	key := func(cidr string, port uint16) vsKey { //nolint:unparam // the call sites show the value
		_, ipNet, err := net.ParseCIDR(cidr)
		Expect(err).ToNot(HaveOccurred())
		return newVsKey(ipNet, port, false)
	}

	snatOf := func(cidr string) string {
		return "add element inet flock vs_prerouting4 { tcp . " + cidr + " . 80 : jump " + vsName(key(cidr, 80)) + "_pre }"
	}

	limitOf := func(cidr string) string {
		return "add element inet flock vs_input4 { tcp . " + cidr + " . 80 : jump " + vsName(key(cidr, 80)) + "_in }"
	}

	setSnat := func(v *vsRules) { v.mark = defaultNmConf.SnatMark }
	clearSnat := clearSnatMark(defaultNmConf)

	BeforeEach(func() {
		ctx = context.Background()
		fake = knftables.NewFake(knftables.InetFamily, nftTable)
		addresses = &fakeMachineIPs{}
		addresses.Set("192.168.1.10", "192.168.2.10", "2001:db8::10")
		rules = newNftRules(fake, defaultNmConf, addresses)
		Expect(rules.setup(ctx)).To(Succeed())
		wildcard = key("0.0.0.0/32", 80)
		concrete = key("192.168.1.10/32", 80)
	})

	It("sets the rules of a wildcard vip on each address of its family, never on the unspecified one", func() {
		Expect(rules.updateVs(ctx, wildcard, setSnat)).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElements(snatOf("192.168.1.10/32"), snatOf("192.168.2.10/32")))
		Expect(fake.Dump()).ToNot(ContainSubstring("0.0.0.0"))
		Expect(fake.Dump()).ToNot(ContainSubstring("2001:db8::10"))
	})

	It("sets an ipv6 wildcard on the ipv6 addresses", func() {
		Expect(rules.updateVs(ctx, key("::/128", 80), setSnat)).To(Succeed())
		Expect(fake.Dump()).To(ContainSubstring("tcp . 2001:db8::10/128 . 80"))
		Expect(fake.Dump()).ToNot(ContainSubstring("192.168.1.10"))
	})

	It("gives every change of the wildcard to each address", func() {
		Expect(rules.updateVs(ctx, wildcard, func(v *vsRules) {
			v.connLimit = &connLimit{max: 100}
			v.rateLimit = &rateLimit{rate: "10/second", burst: 5}
			v.nflogPrefix = "vs-wild"
		})).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElements(limitOf("192.168.1.10/32"), limitOf("192.168.2.10/32")))
		for _, cidr := range []string{"192.168.1.10/32", "192.168.2.10/32"} {
			Expect(rules.vss[key(cidr, 80)].connLimit.max).To(Equal(uint64(100)))
			Expect(rules.vss[key(cidr, 80)].rateLimit.rate).To(Equal("10/second"))
			Expect(rules.vss[key(cidr, 80)].nflogPrefix).To(Equal("vs-wild"))
		}

		Expect(rules.updateVs(ctx, wildcard, func(v *vsRules) { *v = vsRules{} })).To(Succeed())
		Expect(fake.Dump()).ToNot(ContainSubstring("192.168.1.10"))
		Expect(fake.Dump()).ToNot(ContainSubstring("192.168.2.10"))
		Expect(rules.copies).To(BeEmpty())
		Expect(rules.wildcards).To(BeEmpty(), "forgotten once its copies are gone")
	})

	It("follows an address added then removed", func() {
		Expect(rules.updateVs(ctx, wildcard, setSnat)).To(Succeed())

		addresses.Set("192.168.1.10", "192.168.2.10", "192.168.3.10")
		Expect(rules.syncWildcards(ctx)).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElement(snatOf("192.168.3.10/32")))

		addresses.Set("192.168.3.10")
		Expect(rules.syncWildcards(ctx)).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElement(snatOf("192.168.3.10/32")))
		Expect(fake.Dump()).ToNot(ContainSubstring("192.168.1.10"))
		Expect(fake.Dump()).ToNot(ContainSubstring("192.168.2.10"))
	})

	It("does not sync once its ctx is done", func() {
		Expect(rules.updateVs(ctx, wildcard, setSnat)).To(Succeed())
		addresses.Set("192.168.3.10")
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		Expect(rules.syncWildcards(canceled)).To(Succeed())
		Expect(dumpLines(fake)).To(ContainElement(snatOf("192.168.1.10/32")))
	})

	Context("with a concrete vip on one of the addresses", func() {
		It("leaves the address to the concrete vip already there", func() {
			Expect(rules.updateVs(ctx, concrete, func(v *vsRules) { v.nflogPrefix = "vs-conc" })).To(Succeed())
			Expect(rules.updateVs(ctx, wildcard, setSnat)).To(Succeed())
			Expect(rules.vss[concrete]).To(Equal(vsRules{nflogPrefix: "vs-conc"}))
			Expect(dumpLines(fake)).To(ContainElement(snatOf("192.168.2.10/32")))
		})

		It("gives the address to a concrete vip coming later, and takes it back once its rules are gone", func() {
			Expect(rules.updateVs(ctx, wildcard, func(v *vsRules) { v.connLimit = &connLimit{max: 100} })).To(Succeed())

			// a concrete vip with no rule leaves the copy alone, with no nft transaction
			lastTransaction := fake.LastTransaction
			Expect(rules.updateVs(ctx, concrete, clearSnat)).To(Succeed())
			Expect(fake.LastTransaction).To(BeIdenticalTo(lastTransaction))
			Expect(rules.vss[concrete].connLimit).ToNot(BeNil())

			Expect(rules.updateVs(ctx, concrete, setSnat)).To(Succeed())
			Expect(rules.vss[concrete]).To(Equal(vsRules{mark: defaultNmConf.SnatMark}))

			// a change of the wildcard or of the addresses keeps the address with the concrete vip
			Expect(rules.updateVs(ctx, wildcard, func(v *vsRules) { v.nflogPrefix = "vs-wild" })).To(Succeed())
			Expect(rules.syncWildcards(ctx)).To(Succeed())
			Expect(rules.vss[concrete]).To(Equal(vsRules{mark: defaultNmConf.SnatMark}))

			Expect(rules.updateVs(ctx, concrete, clearSnat)).To(Succeed())
			Expect(rules.vss[concrete].connLimit.max).To(Equal(uint64(100)))
			Expect(rules.vss[concrete].nflogPrefix).To(Equal("vs-wild"))
		})

		It("keeps the copy beside a concrete vip on another port", func() {
			Expect(rules.updateVs(ctx, key("192.168.1.10/32", 443), setSnat)).To(Succeed())
			Expect(rules.updateVs(ctx, wildcard, setSnat)).To(Succeed())
			Expect(dumpLines(fake)).To(ContainElement(snatOf("192.168.1.10/32")))
		})
	})
})
