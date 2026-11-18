package ipams_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/seagalia/flock/controller/ipams"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
)

var _ = Describe("Remote", func() {
	var (
		server   *httptest.Server
		received map[string]map[string]any
		headers  http.Header
		reply    func(w http.ResponseWriter)
		remote   *Remote
		aZone    = &zone.Zone{UUID: "zone-1", Name: "production", Pools: []*zone.IpPool{{Name: "default", Subnets: []*zone.Subnet{{Ip: "10.0.0.0", PrefixLength: 24}}}}}
		ctx      = context.Background()
	)

	BeforeEach(func() {
		received = make(map[string]map[string]any)
		reply = func(w http.ResponseWriter) {}
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.Method).To(Equal(http.MethodPost))
			body := make(map[string]any)
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			received[r.URL.Path] = body
			headers = r.Header
			reply(w)
		}))
		DeferCleanup(server.Close)
		var err error
		remote, err = NewRemote(server.URL+"/", false, map[string][]string{"Authorization": {"Bearer tok"}})
		Expect(err).ToNot(HaveOccurred())
	})

	It("should refuse an invalid url", func() {
		_, err := NewRemote("ftp://ipam.local", false, nil)
		Expect(err).To(HaveOccurred())
	})

	It("should acquire an ip of the zone with the configured headers", func() {
		reply = func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"ip": "10.0.0.42"}`))
		}

		ip, err := remote.AcquireIP(ctx, aZone, "default", "vip-1")

		Expect(err).ToNot(HaveOccurred())
		Expect(ip.String()).To(Equal("10.0.0.42"))
		body := received["/v1/ipam/acquire"]
		Expect(body).To(HaveKeyWithValue("zone_uuid", "zone-1"))
		Expect(body).To(HaveKeyWithValue("owner", "vip-1"))
		Expect(body).To(HaveKeyWithValue("pool", "default"))
		Expect(body["zone"]).To(HaveKeyWithValue("name", "production"))
		Expect(headers.Get("Authorization")).To(Equal("Bearer tok"))
	})

	It("should refuse an invalid ip given by the remote", func() {
		reply = func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"ip": "nope"}`))
		}
		_, err := remote.AcquireIP(ctx, aZone, "default", "vip-1")
		Expect(err).To(MatchError(ContainSubstring("invalid IP")))
	})

	It("should send acquire specific, release and release owner", func() {
		ip := netip.MustParseAddr("10.0.0.7")
		Expect(remote.AcquireSpecificIP(ctx, aZone, ip, "vip-1")).To(Succeed())
		Expect(remote.ReleaseIP(ctx, aZone, ip)).To(Succeed())
		Expect(remote.ReleaseOwner(ctx, "vip-1")).To(Succeed())

		Expect(received["/v1/ipam/acquire-specific"]).To(HaveKeyWithValue("ip", "10.0.0.7"))
		Expect(received["/v1/ipam/release"]).To(HaveKeyWithValue("ip", "10.0.0.7"))
		Expect(received["/v1/ipam/release-owner"]).To(Equal(map[string]any{"owner": "vip-1"}))
	})

	It("should tell an ip the remote does not know", func() {
		reply = func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusNotFound)
		}
		Expect(remote.ReleaseIP(ctx, aZone, netip.MustParseAddr("10.0.0.7"))).To(MatchError(ErrIPNotAllocated))
	})

	It("should not call the remote for a zone without subnets nor ip ranges", func() {
		public := &zone.Zone{UUID: "public", Name: "public"}

		Expect(remote.AcquireSpecificIP(ctx, public, netip.MustParseAddr("1.2.3.4"), "vip-1")).To(Succeed())
		Expect(remote.ReleaseIP(ctx, public, netip.MustParseAddr("1.2.3.4"))).To(Succeed())
		_, err := remote.AcquireIP(ctx, public, "default", "vip-1")
		Expect(err).To(MatchError(ErrNoAllocatableIP))
		Expect(received).To(BeEmpty())
	})

	It("should tell allocations of a zone", func() {
		reply = func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"has_allocations": true}`))
		}
		Expect(remote.HasAllocatedIPs(ctx, aZone)).To(BeTrue())
	})

	It("should map a conflict on a specific ip to ErrIPAllocated with the error of the remote", func() {
		reply = func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error": "ip owned by vip-2"}`))
		}
		err := remote.AcquireSpecificIP(ctx, aZone, netip.MustParseAddr("10.0.0.7"), "vip-1")
		Expect(errors.Is(err, ErrIPAllocated)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("remote IPAM HTTP 409: ip owned by vip-2")))
	})

	It("should map a conflict on an automatic acquire to ErrPoolExhausted", func() {
		reply = func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error": "no free ip"}`))
		}
		_, err := remote.AcquireIP(ctx, aZone, "default", "vip-1")
		Expect(errors.Is(err, ErrPoolExhausted)).To(BeTrue())
	})

	It("should give a plain error for another refusal", func() {
		reply = func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error": "denied"}`))
		}
		err := remote.AcquireSpecificIP(ctx, aZone, netip.MustParseAddr("10.0.0.7"), "vip-1")
		Expect(err).To(MatchError("remote IPAM HTTP 403: denied"))
	})

	It("should send the asked pool when acquiring", func() {
		reply = func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"ip": "10.0.0.42"}`))
		}
		_, err := remote.AcquireIP(ctx, aZone, "b", "vip-1")
		Expect(err).ToNot(HaveOccurred())
		Expect(received["/v1/ipam/acquire"]).To(HaveKeyWithValue("pool", "b"))
	})

	Describe("SyncZone", func() {
		It("should post the zone to the sync endpoint", func() {
			Expect(remote.SyncZone(ctx, aZone)).To(Succeed())

			body := received["/v1/ipam/sync-zone"]
			Expect(body).To(HaveKeyWithValue("zone_uuid", "zone-1"))
			Expect(body["zone"]).To(HaveKeyWithValue("name", "production"))
			Expect(body["zone"]).To(HaveKey("pools"))
		})

		It("should send a zone without pools too", func() {
			unmanaged := &zone.Zone{UUID: "zone-2", Name: "public"}

			Expect(remote.SyncZone(ctx, unmanaged)).To(Succeed())

			Expect(received["/v1/ipam/sync-zone"]).To(HaveKeyWithValue("zone_uuid", "zone-2"))
		})

		It("should map a conflict to ErrIPsOutOfRanges", func() {
			reply = func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error": "ip 10.0.0.7 is outside the ranges"}`))
			}

			err := remote.SyncZone(ctx, aZone)

			Expect(errors.Is(err, ErrIPsOutOfRanges)).To(BeTrue())
		})

		It("should give a plain error for a server error", func() {
			reply = func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error": "boom"}`))
			}

			err := remote.SyncZone(ctx, aZone)

			Expect(err).To(MatchError("remote IPAM HTTP 500: boom"))
			Expect(errors.Is(err, ErrIPsOutOfRanges)).To(BeFalse())
		})
	})
})
