// licensed to metallb authors under the apache license, version 2.0
// file: https://github.com/metallb/metallb/tree/main/internal/layer2/announcer_test.go

package layer2

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/seagalia/flock/sdk/logging"
)

func Test_SetBalancer_AddsToAnnouncedServices(t *testing.T) {
	announce := &Announce{
		logger:   logging.Component("announcer"),
		ips:      map[string][]IPAdvertisement{},
		ipRefcnt: map[string]int{},
		spamCh:   make(chan IPAdvertisement, 1),
	}

	services := []struct {
		name string
		adv  IPAdvertisement
	}{
		{
			name: "foo",
			adv: IPAdvertisement{
				ip:            netip.MustParseAddr("192.168.1.20"),
				interfaces:    make([]string, 0),
				allInterfaces: true,
			},
		},
		{
			name: "foo",
			adv: IPAdvertisement{
				ip:            netip.MustParseAddr("1000::1"),
				interfaces:    []string{"eth0"},
				allInterfaces: true,
			},
		},
		{
			name: "bar",
			adv: IPAdvertisement{
				ip:            netip.MustParseAddr("192.168.1.20"),
				interfaces:    []string{"eth1"},
				allInterfaces: false,
			},
		},
	}

	for _, service := range services {
		announce.SetBalancer(service.name, service.adv)
		// We need to empty spamCh as spamLoop() is not started.
		<-announce.spamCh

		if !announce.AnnounceName(service.name) {
			t.Fatalf("service %v is not announced", service.name)
		}
	}
	if len(announce.ips["foo"]) != 2 {
		t.Fatalf("service foo has more than 2 ips: %d", len(announce.ips["foo"]))
	}
	if announce.ipRefcnt["192.168.1.20"] != 2 {
		t.Fatalf("ip 192.168.1.20 has not 2 refcnt: %d", announce.ipRefcnt["192.168.1.20"])
	}
}

// runStopped runs announce with a ctx already ended, so it returns at once and scans no interface of the host
func runStopped(announce *Announce) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	announce.Run(ctx)
}

func Test_Stop_ClosesResponders(t *testing.T) {
	resp, _, cleanup := newTestARP(t, func(netip.Addr, string) dropReason { return dropReasonNone })
	defer cleanup()
	announce := New(false)
	announce.arps[1] = resp
	runStopped(announce)

	select {
	case <-resp.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("the ARP responder still answers after the announcer stopped")
	}
}

func Test_Stop_DoSpamDoesNotBlock(t *testing.T) {
	announce := New(false)
	runStopped(announce)
	adv := IPAdvertisement{ip: netip.MustParseAddr("192.168.1.20"), allInterfaces: true}
	// fills the buffer, nothing reads it once stopped
	for range cap(announce.spamCh) {
		announce.spamCh <- adv
	}

	sent := make(chan struct{})
	go func() {
		defer close(sent)
		announce.doSpam(adv)
	}()
	select {
	case <-sent:
	case <-time.After(2 * time.Second):
		t.Fatal("doSpam blocks after the announcer stopped")
	}
}
