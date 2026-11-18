package nm

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"sigs.k8s.io/knftables"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/agent/starling/l3logs"
	"github.com/seagalia/flock/sdk/logging"
)

// nftTable is the table of the agent: its base chains run before the iptables ones of the same hooks,
// a drop there is final and an accept there doesn't skip the rules of the other tables
const nftTable = "flock"

const (
	preroutingChain  = "prerouting"
	inputChain       = "input"
	outputChain      = "output"
	postroutingChain = "postrouting"
)

// the vs maps send the packets of a vip and port to the chain of its virtual server,
// interval keys so that a prefix vip matches every address of its prefix
const (
	preroutingMap4 = "vs_prerouting4"
	preroutingMap6 = "vs_prerouting6"
	inputMap4      = "vs_input4"
	inputMap6      = "vs_input6"
)

func baseChain(name string, chainType knftables.BaseChainType, hook knftables.BaseChainHook, priority string) *knftables.Chain {
	return &knftables.Chain{
		Name:     name,
		Type:     new(chainType),
		Hook:     new(hook),
		Priority: new(knftables.BaseChainPriority(priority)),
		Policy:   knftables.PtrTo(knftables.AcceptPolicy),
	}
}

// prerouting runs after conntrack (-200) so rules can match the connection state
func preroutingBaseChain() *knftables.Chain {
	return baseChain(preroutingChain, knftables.FilterType, knftables.PreroutingHook, "mangle - 5")
}

func inputBaseChain() *knftables.Chain {
	return baseChain(inputChain, knftables.FilterType, knftables.InputHook, "filter - 10")
}

func outputBaseChain() *knftables.Chain {
	return baseChain(outputChain, knftables.FilterType, knftables.OutputHook, "filter - 10")
}

func postroutingBaseChain() *knftables.Chain {
	return baseChain(postroutingChain, knftables.NATType, knftables.PostroutingHook, "srcnat - 10")
}

func vsMap(name, addrType string) *knftables.Map {
	return &knftables.Map{
		Name:  name,
		Type:  fmt.Sprintf("inet_proto . %s . inet_service : verdict", addrType),
		Flags: []knftables.SetFlag{knftables.IntervalFlag},
	}
}

// bogusPacketRules drop packets no client sends
var bogusPacketRules = []string{
	"ct state invalid drop",
	"tcp flags & (fin|syn|rst|ack) != syn ct state new drop",
	"ct state new tcp option maxseg size != 536-65535 drop",
	"tcp flags & (fin|syn) == fin|syn drop",
	"tcp flags & (syn|rst) == syn|rst drop",
	"tcp flags & (fin|rst) == fin|rst drop",
	"tcp flags & (fin|ack) == fin drop",
	"tcp flags & (ack|urg) == urg drop",
	"tcp flags & (ack|psh) == psh drop",
	"tcp flags & (fin|syn|rst|psh|ack|urg) == 0x0 drop",
}

type nftRules struct {
	entry zerolog.Logger
	nft   knftables.Interface
	conf  config.NetworkManagerConfig
	// addresses expands the wildcard vips, nil expands them to nothing
	addresses MachineIPs

	// mu serializes the handlers' changes, each one computed from vss
	mu  sync.Mutex
	vss map[vsKey]vsRules
	// wildcards are the rules asked for a wildcard vip, set on each address of the machine instead of in nft
	wildcards map[vsKey]vsRules
	// copies are the keys whose rules come from a wildcard, with the key of their wildcard
	copies map[vsKey]vsKey
}

func newNftRules(nft knftables.Interface, conf config.NetworkManagerConfig, addresses MachineIPs) *nftRules {
	return &nftRules{
		entry:     logging.Component("network_manager"),
		nft:       nft,
		conf:      conf,
		addresses: addresses,
		vss:       map[vsKey]vsRules{},
		wildcards: map[vsKey]vsRules{},
		copies:    map[vsKey]vsKey{},
	}
}

// setup leaves the postrouting rules to addSnatSources
func (r *nftRules) setup(ctx context.Context) error {
	tx := r.nft.NewTransaction()
	tx.Add(&knftables.Table{})
	tx.Add(vsMap(preroutingMap4, "ipv4_addr"))
	tx.Add(vsMap(preroutingMap6, "ipv6_addr"))
	tx.Add(vsMap(inputMap4, "ipv4_addr"))
	tx.Add(vsMap(inputMap6, "ipv6_addr"))
	tx.Add(postroutingBaseChain())
	for _, chain := range []*knftables.Chain{preroutingBaseChain(), inputBaseChain(), outputBaseChain()} {
		tx.Add(chain)
		tx.Flush(chain)
	}

	if !r.conf.DisableInitRules {
		for _, rule := range bogusPacketRules {
			tx.Add(&knftables.Rule{Chain: preroutingChain, Rule: rule})
		}
		// only the first packets of a connection need the agent rules
		tx.Add(&knftables.Rule{Chain: inputChain, Rule: "ct state established,related accept"})
		tx.Add(&knftables.Rule{Chain: outputChain, Rule: "ct state established,related accept"})
	}

	tx.Add(&knftables.Rule{Chain: preroutingChain, Rule: fmt.Sprintf("meta l4proto . ip daddr . th dport vmap @%s", preroutingMap4)})
	tx.Add(&knftables.Rule{Chain: preroutingChain, Rule: fmt.Sprintf("meta l4proto . ip6 daddr . th dport vmap @%s", preroutingMap6)})
	tx.Add(&knftables.Rule{Chain: inputChain, Rule: fmt.Sprintf("meta l4proto . ip daddr . th dport vmap @%s", inputMap4)})
	tx.Add(&knftables.Rule{Chain: inputChain, Rule: fmt.Sprintf("meta l4proto . ip6 daddr . th dport vmap @%s", inputMap6)})

	logMarks := fmt.Sprintf("meta mark { %d, %d }", r.conf.SnatLogMark, r.conf.LogOnlyMark)
	tx.Add(&knftables.Rule{Chain: outputChain, Rule: fmt.Sprintf(
		"%s tcp flags & (fin|syn|rst|ack) == syn log group %d", logMarks, l3logs.DefaultNflogGroup)})
	tx.Add(&knftables.Rule{Chain: outputChain, Rule: fmt.Sprintf(
		"%s meta l4proto udp log group %d", logMarks, l3logs.DefaultNflogGroup)})

	return r.nft.Run(ctx, tx)
}

type snatSource struct {
	iface string
	ip    net.IP
}

func addSnatSources(ctx context.Context, nft knftables.Interface, conf config.NetworkManagerConfig, sources []snatSource) error {
	tx := nft.NewTransaction()
	tx.Add(&knftables.Table{})
	tx.Add(postroutingBaseChain())
	tx.Flush(postroutingBaseChain())
	snatMarks := fmt.Sprintf("meta mark { %d, %d }", conf.SnatMark, conf.SnatLogMark)
	for _, source := range sources {
		family := "ip"
		if source.ip.To4() == nil {
			family = "ip6"
		}
		tx.Add(&knftables.Rule{Chain: postroutingChain, Rule: fmt.Sprintf(
			"oifname %q %s snat %s to %s", source.iface, snatMarks, family, source.ip)})
	}
	return nft.Run(ctx, tx)
}

// deleteTable adds the table before deleting it: deleting a missing table fails,
// and nft destroy needs a kernel and nft too recent to rely on
func deleteTable(ctx context.Context, nft knftables.Interface) error {
	tx := nft.NewTransaction()
	tx.Add(&knftables.Table{})
	tx.Delete(&knftables.Table{})
	return nft.Run(ctx, tx)
}

type vsKey struct {
	cidr  string
	port  uint16
	isUdp bool
}

func newVsKey(ipNet *net.IPNet, port uint16, isUdp bool) vsKey {
	return vsKey{cidr: ipNet.String(), port: port, isUdp: isUdp}
}

func (k vsKey) protocol() string {
	if k.isUdp {
		return "udp"
	}
	return "tcp"
}

func (k vsKey) addressFamily() (family, addrType string) {
	if strings.Contains(k.cidr, ":") {
		return "ip6", "ipv6_addr"
	}
	return "ip", "ipv4_addr"
}

func (k vsKey) maps() (prerouting, input string) {
	if strings.Contains(k.cidr, ":") {
		return preroutingMap6, inputMap6
	}
	return preroutingMap4, inputMap4
}

func (k vsKey) element(mapName, chain string) *knftables.Element {
	return &knftables.Element{
		Map:   mapName,
		Key:   []string{k.protocol(), k.cidr, fmt.Sprint(k.port)},
		Value: []string{fmt.Sprintf("jump %s", chain)},
	}
}

// vsName gives the prefix of the chains and sets of a virtual server, e.g. vs_10_250_0_70_32_80_tcp
func vsName(k vsKey) string {
	name := strings.NewReplacer("/", "_", ":", "_", ".", "_").Replace(k.cidr)
	return fmt.Sprintf("vs_%s_%d_%s", name, k.port, k.protocol())
}

type connLimit struct {
	max           uint64
	bySource      bool
	byDestination bool
}

type rateLimit struct {
	rate          string
	burst         uint64
	bySource      bool
	byDestination bool
}

// vsRules is what the agent applies to the packets of a virtual server, the zero value is nothing
type vsRules struct {
	mark        uint
	nflogPrefix string
	connLimit   *connLimit
	rateLimit   *rateLimit
}

const rateCounterTimeout = time.Minute

// clearSnatMark keeps a log-only mark: handlers clear the snat of a vs without knowing its other marks
func clearSnatMark(conf config.NetworkManagerConfig) func(*vsRules) {
	return func(v *vsRules) {
		isSnatMark := v.mark == conf.SnatMark || v.mark == conf.SnatLogMark
		if isSnatMark {
			v.mark = 0
		}
	}
}

// clearLogOnlyMark keeps a snat mark: handlers clear the log-only mark of every vs without l3 log
func clearLogOnlyMark(conf config.NetworkManagerConfig) func(*vsRules) {
	return func(v *vsRules) {
		if v.mark == conf.LogOnlyMark {
			v.mark = 0
		}
	}
}

// updateVs applies change to the rules of a virtual server in one transaction;
// the change is kept only when nft applied it, so a retry sends it again
func (r *nftRules) updateVs(ctx context.Context, key vsKey, change func(rules *vsRules)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key.isWildcard() {
		return r.updateWildcard(ctx, key, change)
	}
	return r.updateConcrete(ctx, key, change)
}

// apply puts next in place of the rules of key in one transaction. r.mu must be held.
func (r *nftRules) apply(ctx context.Context, key vsKey, next vsRules) error {
	current := r.vss[key]
	// handlers clear the l3 log of every vs on each update: most changes keep the rules
	if next == current {
		return nil
	}

	tx := r.nft.NewTransaction()
	syncPrerouting(tx, key, current, next)
	syncInput(tx, key, current, next)
	if tx.NumOperations() > 0 {
		err := r.nft.Run(ctx, tx)
		if err != nil {
			return err
		}
	}

	if next == (vsRules{}) {
		delete(r.vss, key)
		return nil
	}
	r.vss[key] = next
	return nil
}

func syncPrerouting(tx *knftables.Transaction, key vsKey, current, next vsRules) {
	chain := &knftables.Chain{Name: vsName(key) + "_pre"}
	preroutingMap, _ := key.maps()
	element := key.element(preroutingMap, chain.Name)

	if next.mark == 0 {
		if current.mark != 0 {
			deleteVsChain(tx, chain, element)
		}
		return
	}
	tx.Add(chain)
	tx.Flush(chain)
	tx.Add(&knftables.Rule{Chain: chain.Name, Rule: fmt.Sprintf("meta mark set %d", next.mark)})
	tx.Add(element)
}

// syncInput rewrites the rules of the vs chain but keeps the sets both models use:
// they hold the per address counters of the limits
func syncInput(tx *knftables.Transaction, key vsKey, current, next vsRules) {
	chain := &knftables.Chain{Name: vsName(key) + "_in"}
	_, inputMap := key.maps()
	element := key.element(inputMap, chain.Name)
	currentSets := vsInputSets(key, current)
	nextSets := vsInputSets(key, next)
	nextRules := vsInputRules(key, next)

	if len(nextRules) == 0 {
		if len(vsInputRules(key, current)) > 0 {
			deleteVsChain(tx, chain, element)
			deleteSets(tx, currentSets)
		}
		return
	}
	tx.Add(chain)
	tx.Flush(chain)
	deleteSets(tx, setsMissingFrom(currentSets, nextSets))
	for _, set := range nextSets {
		tx.Add(set)
	}
	for _, rule := range nextRules {
		tx.Add(&knftables.Rule{Chain: chain.Name, Rule: rule})
	}
	tx.Add(element)
}

// deleteVsChain removes the jump to the chain first: nft refuses to delete a chain still referenced or not empty
func deleteVsChain(tx *knftables.Transaction, chain *knftables.Chain, element *knftables.Element) {
	tx.Delete(&knftables.Element{Map: element.Map, Key: element.Key})
	tx.Flush(chain)
	tx.Delete(chain)
}

func deleteSets(tx *knftables.Transaction, sets []*knftables.Set) {
	for _, set := range sets {
		tx.Delete(&knftables.Set{Name: set.Name})
	}
}

func setsMissingFrom(sets, from []*knftables.Set) []*knftables.Set {
	var missing []*knftables.Set
	for _, set := range sets {
		found := false
		for _, other := range from {
			found = found || other.Name == set.Name
		}
		if !found {
			missing = append(missing, set)
		}
	}
	return missing
}

// vsInputRules gives the rules of the vs input chain: l3 log first so it sees the connections the limits drop
func vsInputRules(key vsKey, v vsRules) []string {
	name := vsName(key)
	family, addrType := key.addressFamily()
	var rules []string
	if v.nflogPrefix != "" {
		logRule := fmt.Sprintf("log prefix %q group %d", v.nflogPrefix, l3logs.DefaultNflogGroup)
		if !key.isUdp {
			logRule = fmt.Sprintf("tcp flags & (fin|syn|rst|ack) == syn %s", logRule)
		}
		rules = append(rules, logRule)
	}
	if v.connLimit != nil {
		rules = append(rules, connLimitRules(name, family, *v.connLimit)...)
	}
	if v.rateLimit != nil {
		rules = append(rules, rateLimitRule(name, family, addrType, key.isUdp, *v.rateLimit))
	}
	return rules
}

// connLimitRules gives one rule per source and one per destination when both are asked: each address keeps its own count
func connLimitRules(name, family string, limit connLimit) []string {
	over := fmt.Sprintf("ct count over %d", limit.max)
	if !limit.bySource && !limit.byDestination {
		return []string{fmt.Sprintf("ct state new %s drop", over)}
	}
	var rules []string
	if limit.bySource {
		rules = append(rules, fmt.Sprintf("ct state new update @%s_conn_src { %s saddr %s } drop", name, family, over))
	}
	if limit.byDestination {
		rules = append(rules, fmt.Sprintf("ct state new update @%s_conn_dst { %s daddr %s } drop", name, family, over))
	}
	return rules
}

func rateLimitRule(name, family, addrType string, isUdp bool, limit rateLimit) string {
	limitExpr := fmt.Sprintf("limit rate over %s", limit.rate)
	if limit.burst > 0 {
		limitExpr += fmt.Sprintf(" burst %d packets", limit.burst)
	}
	newConnection := "ct state new "
	if isUdp {
		newConnection = ""
	}
	suffix, keyExpr, _ := rateKey(family, addrType, limit)
	if suffix == "" {
		return fmt.Sprintf("%s%s drop", newConnection, limitExpr)
	}
	return fmt.Sprintf("%supdate @%s_rate_%s { %s %s } drop", newConnection, name, suffix, keyExpr, limitExpr)
}

// rateKey gives an empty suffix for a global limit
func rateKey(family, addrType string, limit rateLimit) (suffix, keyExpr, setType string) {
	switch {
	case limit.bySource && limit.byDestination:
		return "srcdst", fmt.Sprintf("%s saddr . %s daddr", family, family), fmt.Sprintf("%s . %s", addrType, addrType)
	case limit.bySource:
		return "src", fmt.Sprintf("%s saddr", family), addrType
	case limit.byDestination:
		return "dst", fmt.Sprintf("%s daddr", family), addrType
	}
	return "", "", ""
}

func vsInputSets(key vsKey, v vsRules) []*knftables.Set {
	name := vsName(key)
	family, addrType := key.addressFamily()
	dynamic := []knftables.SetFlag{knftables.DynamicFlag}
	var sets []*knftables.Set
	// the kernel drops a conn counter itself once its connections are gone
	if v.connLimit != nil && v.connLimit.bySource {
		sets = append(sets, &knftables.Set{Name: name + "_conn_src", Type: addrType, Flags: dynamic})
	}
	if v.connLimit != nil && v.connLimit.byDestination {
		sets = append(sets, &knftables.Set{Name: name + "_conn_dst", Type: addrType, Flags: dynamic})
	}
	if v.rateLimit != nil {
		suffix, _, setType := rateKey(family, addrType, *v.rateLimit)
		if suffix != "" {
			sets = append(sets, &knftables.Set{
				Name:    fmt.Sprintf("%s_rate_%s", name, suffix),
				Type:    setType,
				Flags:   dynamic,
				Timeout: new(rateCounterTimeout),
			})
		}
	}
	return sets
}
