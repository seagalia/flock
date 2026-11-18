// licensed to metallb authors under the apache license, version 2.0
// file: https://github.com/metallb/metallb/tree/main/internal/layer2/announcer.go

package layer2

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/logging"
)

// Various reasons why a packet was dropped.
const (
	dropReasonNone dropReason = iota
	dropReasonClosed
	dropReasonError
	dropReasonARPReply
	dropReasonMessageType
	dropReasonNoSourceLL
	dropReasonEthernetDestination
	dropReasonAnnounceIP
	dropReasonNotMatchInterface
)

// Announce is used to "announce" new IPs mapped to the node's MAC address.
type Announce struct {
	logger zerolog.Logger

	sync.RWMutex
	nodeInterfaces    []string // current local interfaces' name list
	arps              map[int]*arpResponder
	ndps              map[int]*ndpResponder
	ips               map[string][]IPAdvertisement // svcName -> IPAdvertisements
	ipRefcnt          map[string]int               // ip.String() -> number of uses
	disableGratuitous bool

	// This channel can block - do not write to it while holding the mutex
	// to avoid deadlocking.
	spamCh chan IPAdvertisement

	stopped chan struct{}
}

// New returns an initialized Announce, which answers and announces once Run runs.
func New(disableGratuitous bool) *Announce {
	return &Announce{
		logger:            logging.Component("announcer"),
		nodeInterfaces:    []string{},
		arps:              map[int]*arpResponder{},
		ndps:              map[int]*ndpResponder{},
		ips:               map[string][]IPAdvertisement{},
		ipRefcnt:          map[string]int{},
		spamCh:            make(chan IPAdvertisement, 1024),
		stopped:           make(chan struct{}),
		disableGratuitous: disableGratuitous,
	}
}

// Run answers ARP/NDP and sends the gratuitous announces until ctx ends. It then closes the responders, so the
// neighbours stop sending the traffic of the vips to this node and turn to the agent announcing them next.
func (a *Announce) Run(ctx context.Context) {
	defer close(a.stopped)
	var loops sync.WaitGroup
	loops.Go(func() { a.interfaceScan(ctx) })
	loops.Go(func() { a.spamLoop(ctx) })
	loops.Wait()
	a.closeResponders()
}

func (a *Announce) closeResponders() {
	a.Lock()
	defer a.Unlock()
	for i, client := range a.arps {
		err := client.Close()
		if err != nil {
			a.logger.Warn().Err(err).Str("interface", client.Interface()).Msg("failed to close ARP responder")
		}
		delete(a.arps, i)
	}
	for i, client := range a.ndps {
		err := client.Close()
		if err != nil {
			a.logger.Warn().Err(err).Str("interface", client.Interface()).Msg("failed to close NDP responder")
		}
		delete(a.ndps, i)
	}
	a.logger.Info().Msg("ARP and NDP responders closed")
}

func (a *Announce) interfaceScan(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	// a scan after the stop would open responders again
	for ctx.Err() == nil {
		err := a.updateInterfaces()
		if err != nil {
			a.logger.Error().Err(err).Msg("failed to update interfaces")
		}
		ticker.Reset(10 * time.Second)
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (a *Announce) updateInterfaces() error {
	ifs, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("getting interfaces: %w", err)
	}

	a.Lock()
	defer a.Unlock()

	keepARP, keepNDP := map[int]bool{}, map[int]bool{}
	curIfs := make([]string, 0, len(ifs))
	for _, intf := range ifs {
		ifi := intf
		curIfs = append(curIfs, ifi.Name)
		l := a.logger.With().Str("interface", ifi.Name).Logger()
		addrs, err := ifi.Addrs()
		if err != nil {
			return fmt.Errorf("getting addresses for interface: %w", err)
		}

		if ifi.Flags&net.FlagUp == 0 {
			continue
		}
		if _, err = os.Stat("/sys/class/net/" + ifi.Name + "/master"); !os.IsNotExist(err) {
			continue
		}
		f, err := os.ReadFile("/sys/class/net/" + ifi.Name + "/flags")
		if err == nil {
			flags, _ := strconv.ParseUint(string(f)[:len(string(f))-1], 0, 32)
			// NOARP flag
			if flags&0x80 != 0 {
				continue
			}
		}
		if ifi.Flags&net.FlagBroadcast != 0 {
			keepARP[ifi.Index] = true
		}

		for _, a := range addrs {
			ipaddr, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ipaddr.IP.To4() != nil || !ipaddr.IP.IsLinkLocalUnicast() {
				continue
			}
			keepNDP[ifi.Index] = true
			break
		}

		if keepARP[ifi.Index] && a.arps[ifi.Index] == nil {
			resp, err := newARPResponder(&ifi, a.shouldAnnounce)
			if err != nil {
				l.Trace().Msgf("Skipping creating ARP responder due to error: %s", err.Error())
			} else {
				a.arps[ifi.Index] = resp
				l.Info().Str("event", "createARPResponder").Msg("created ARP responder for interface")
			}
		}
		if keepNDP[ifi.Index] && a.ndps[ifi.Index] == nil {
			resp, err := newNDPResponder(&ifi, a.shouldAnnounce)
			if err != nil {
				l.Trace().Msgf("Skipping creating NDP responder due to error: %s", err.Error())
			} else {
				a.ndps[ifi.Index] = resp
				l.Info().Str("event", "createNDPResponder").Msg("created NDP responder for interface")
			}
		}
	}

	a.nodeInterfaces = curIfs

	for i, client := range a.arps {
		if !keepARP[i] {
			client.Close()
			delete(a.arps, i)
			a.logger.Info().
				Str("interface", client.Interface()).
				Str("event", "deleteARPResponder").
				Msg("deleted ARP responder for interface")
		}
	}
	for i, client := range a.ndps {
		if !keepNDP[i] {
			client.Close()
			delete(a.ndps, i)
			a.logger.Info().
				Str("interface", client.Interface()).
				Str("event", "deleteNDPResponder").
				Msg("deleted NDP responder for interface")
		}
	}
	return nil
}

func (a *Announce) spamLoop(ctx context.Context) {
	// Map IP to spam stop time.
	type timedSpam struct {
		until time.Time
		IPAdvertisement
	}
	m := map[string]timedSpam{}
	// We can't create a stopped ticker, so create one with a big period to avoid ticking for nothing
	ticker := time.NewTicker(time.Hour)
	ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case s := <-a.spamCh:
			if len(m) == 0 {
				// See https://github.com/metallb/metallb/issues/172 for the 1100 choice.
				ticker.Reset(1100 * time.Millisecond)
			}
			ipStr := s.ip.String()
			_, ok := m[ipStr]
			m[ipStr] = timedSpam{time.Now().Add(5 * time.Second), s}
			if !ok {
				// Spam right away to avoid waiting up to 1100 milliseconds even if
				// it means we call gratuitous() twice in a row in a short amount of time.
				a.gratuitous(s)
			}
		case now := <-ticker.C:
			for ipStr, tSpam := range m {
				if now.After(tSpam.until) {
					delete(m, ipStr)
				} else {
					a.gratuitous(tSpam.IPAdvertisement)
				}
			}
			if len(m) == 0 {
				ticker.Stop()
			}
		}
	}
}

func (a *Announce) doSpam(adv IPAdvertisement) {
	select {
	case a.spamCh <- adv:
	case <-a.stopped:
	}
}

func (a *Announce) gratuitous(adv IPAdvertisement) {
	if a.disableGratuitous {
		a.logger.Debug().Str("op", "gratuitousAnnounce").Msg("gratuitous ARP/NDP disabled, skipping")
		return
	}
	a.RLock()
	defer a.RUnlock()
	ip := adv.ip
	if a.ipRefcnt[ip.String()] <= 0 {
		// We've lost control of the IP, someone else is
		// doing announcements.
		return
	}
	lentry := a.logger.With().Str("op", "gratuitousAnnounce").Stringer("ip", ip).Logger()
	if ip.Is4() {
		lentry = lentry.With().Str("type", "arp").Logger()
		for _, client := range a.arps {
			centry := lentry.With().Str("iface", client.intf).Logger()
			if !adv.matchInterface(client.intf) {
				centry.Debug().Msg("skip interfaces")
				continue
			}
			if err := client.Gratuitous(ip); err != nil {
				centry.Error().
					Err(err).
					Msg("failed to make gratuitous ARP announcement")
			}
			centry.Info().Msg("send for interfaces")
		}
	} else {
		lentry = lentry.With().Str("type", "ndp").Logger()
		for _, client := range a.ndps {
			centry := lentry.With().Str("iface", client.intf).Logger()
			if !adv.matchInterface(client.intf) {
				centry.Debug().Msg("skip interfaces")
				continue
			}
			if err := client.Gratuitous(ip); err != nil {
				centry.Error().
					Err(err).
					Msg("failed to make gratuitous NDP announcement")
			}
			centry.Info().Msg("send for interfaces")
		}
	}
}

func (a *Announce) shouldAnnounce(ip netip.Addr, intf string) dropReason {
	a.RLock()
	defer a.RUnlock()
	ipFound := false
	for _, ipAdvertisements := range a.ips {
		for _, i := range ipAdvertisements {
			if i.ip.Compare(ip) == 0 {
				ipFound = true
				if i.matchInterface(intf) {
					return dropReasonNone
				}
			}
		}
	}
	if ipFound {
		return dropReasonNotMatchInterface
	}
	return dropReasonAnnounceIP
}

// SetBalancer adds ip to the set of announced addresses.
func (a *Announce) SetBalancer(name string, adv IPAdvertisement) {
	// Call doSpam at the end of the function without holding the lock
	defer a.doSpam(adv)
	a.Lock()
	defer a.Unlock()

	if ipAdvertisements, ok := a.ips[name]; ok {
		for i := range ipAdvertisements {
			if adv.ip.Compare(a.ips[name][i].ip) == 0 {
				a.ips[name][i] = adv // override in case the interface list changed
				return
			}
		}
	}
	a.ips[name] = append(a.ips[name], adv)

	a.ipRefcnt[adv.ip.String()]++
	if a.ipRefcnt[adv.ip.String()] > 1 {
		// Multiple services are using this IP, so there's nothing
		// else to do right now.
		return
	}

	for _, client := range a.ndps {
		if err := client.Watch(adv.ip); err != nil {
			a.logger.Error().
				Err(err).
				Str("op", "watchMulticastGroup").
				Stringer("ip", adv.ip).
				Str("interface", client.intf).
				Msg("failed to watch NDP multicast group for IP, NDP responder will not respond to requests for this address")
		}
	}

	a.logger.Info().
		Str("op", "setBalancer").
		Str("name", name).
		Stringer("ip", adv.ip).
		Msg("Add ip to l2 advertisement")
	stats.AddAnnounced(adv.ip.String())
}

// DeleteBalancer deletes an address from the set of addresses we should announce.
func (a *Announce) DeleteBalancer(name string) {
	a.Lock()
	defer a.Unlock()

	advs, ok := a.ips[name]
	if !ok {
		return
	}
	delete(a.ips, name)

	for _, cur := range advs {
		a.ipRefcnt[cur.ip.String()]--
		if a.ipRefcnt[cur.ip.String()] > 0 {
			// Another service is still using this IP, don't touch any
			// more things.
			continue
		}

		for _, client := range a.ndps {
			if err := client.Unwatch(cur.ip); err != nil {
				a.logger.Error().
					Err(err).
					Str("op", "unwatchMulticastGroup").
					Stringer("ip", cur.ip).
					Str("interface", client.intf).
					Msg("failed to unwatch NDP multicast group for IP")
			}
		}
	}
	a.logger.Info().
		Str("op", "deleteBalancer").
		Str("name", name).
		Msg("Delete ip to l2 advertisement")
	stats.RemoveAnnounced(name)
}

// AnnounceName returns true when we have an announcement under name.
func (a *Announce) AnnounceName(name string) bool {
	a.RLock()
	defer a.RUnlock()
	_, ok := a.ips[name]
	return ok
}

// GetInterfaces returns current interfaces list.
func (a *Announce) GetInterfaces() []string {
	a.Lock()
	defer a.Unlock()

	localInterfaces := make([]string, len(a.nodeInterfaces))
	copy(localInterfaces, a.nodeInterfaces)
	return localInterfaces
}

// dropReason is the reason why a layer2 protocol packet was not
// responded to.
type dropReason int
