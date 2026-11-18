package serves_test

import (
	"fmt"
	"sync"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
)

var _ = Describe("Zone", func() {
	var server *testServer
	BeforeEach(func() {
		server = newTestServer(instantiaters.NewNull())
	})

	Context("CreateZone", func() {
		It("should create zone with a uuid as admin", func() {
			resp, err := server.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: newZone("zone-1")})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetZone().GetName()).To(Equal("zone-1"))
			Expect(resp.GetZone().GetUUID()).ToNot(BeEmpty())

			read, err := server.ReadZone(userCtx(adminUser), &svccore.UuidRequest{UUID: resp.GetZone().GetUUID()})
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetZone().GetZone()).To(Equal("z1"))
		})
		It("should be refused to non admin", func() {
			_, err := server.CreateZone(userCtx("bob"), &svczone.CreateZoneRequest{Zone: newZone("zone-1")})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("should refuse invalid zone", func() {
			client := svczoneconnect.NewZoneClient(server.throughChain())
			_, err := client.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: &zone.Zone{}})
			expectCode(err, connect.CodeInvalidArgument)
		})
		It("should accept a zone without subnets nor ip ranges", func() {
			client := svczoneconnect.NewZoneClient(server.throughChain())
			_, err := client.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: &zone.Zone{Name: "public"}})
			Expect(err).ToNot(HaveOccurred())
		})
		It("should refuse a zone with overlapping pools", func() {
			overlapping := newZone("zone-1")
			overlapping.Pools = append(overlapping.Pools, &zone.IpPool{
				Name: "b", IpRanges: []*zone.IpRange{{IpMin: "10.0.0.10", IpMax: "10.0.0.20"}},
			})

			_, err := server.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: overlapping})

			expectCode(err, connect.CodeInvalidArgument)
		})
		It("should refuse zone with existing name", func() {
			server.createZone("zone-1")

			_, err := server.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: newZone("zone-1")})
			Expect(err).To(HaveOccurred())
		})
	})

	Context("existing zone", func() {
		var zoneUuid string
		BeforeEach(func() {
			zoneUuid = server.createZone("zone-1")
		})

		It("should update zone as admin", func() {
			updated := newZone("zone-1")
			updated.Zone = "z2"

			resp, err := server.UpdateZone(userCtx(adminUser), &svczone.UpdateZoneRequest{UUID: zoneUuid, Zone: updated})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetZone().GetUUID()).To(Equal(zoneUuid))

			read, err := server.ReadZone(userCtx(adminUser), &svccore.UuidRequest{UUID: zoneUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetZone().GetZone()).To(Equal("z2"))
		})
		It("should refuse an update changing the pools", func() {
			changed := newZone("zone-1")
			changed.Pools[0].Subnets = []*zone.Subnet{{Ip: "10.9.0.0", PrefixLength: 24}}

			_, err := server.UpdateZone(userCtx(adminUser), &svczone.UpdateZoneRequest{UUID: zoneUuid, Zone: changed})

			expectCode(err, connect.CodeInvalidArgument)
			Expect(err).To(MatchError(ContainSubstring("add-pool-to-zone")))
		})
		It("should update the name of a zone keeping its pools", func() {
			_, err := server.UpdateZone(userCtx(adminUser), &svczone.UpdateZoneRequest{UUID: zoneUuid, Zone: newZone("zone-renamed")})
			Expect(err).ToNot(HaveOccurred())

			read, err := server.ReadZone(userCtx(adminUser), &svccore.UuidRequest{UUID: zoneUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetZone().GetName()).To(Equal("zone-renamed"))
		})
		It("should refuse update to non admin", func() {
			_, err := server.UpdateZone(userCtx("bob"), &svczone.UpdateZoneRequest{UUID: zoneUuid, Zone: newZone("zone-1")})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("should list zones", func() {
			resp, err := server.ListZone(userCtx(adminUser), &svczone.ListZoneRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetZones()).To(HaveLen(1))
		})
		It("should delete zone as admin", func() {
			_, err := server.DeleteZone(userCtx(adminUser), &svccore.UuidRequest{UUID: zoneUuid})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.ReadZone(userCtx(adminUser), &svccore.UuidRequest{UUID: zoneUuid})
			expectCode(err, connect.CodeNotFound)
		})
		It("should refuse delete to non admin", func() {
			_, err := server.DeleteZone(userCtx("bob"), &svccore.UuidRequest{UUID: zoneUuid})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("should refuse to delete zone used by an instance", func() {
			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.DeleteZone(userCtx(adminUser), &svccore.UuidRequest{UUID: zoneUuid})

			expectCode(err, connect.CodeFailedPrecondition)
		})
		It("should refuse to delete zone with ips still allocated", func() {
			server = newTestServerWithIpam(instantiaters.NewNull(), dbIpam)
			_, err := server.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{
				Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: zoneUuid},
			})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.DeleteZone(userCtx(adminUser), &svccore.UuidRequest{UUID: zoneUuid})

			expectCode(err, connect.CodeFailedPrecondition)
			Expect(err).To(MatchError(ContainSubstring("holds allocated IPs")))

			_, err = server.ReadZone(userCtx(adminUser), &svccore.UuidRequest{UUID: zoneUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(storedRangeCount(zoneUuid)).To(Equal(1))
		})
		It("should free the ranges of a deleted zone in the ipam", func() {
			server = newTestServerWithIpam(instantiaters.NewNull(), dbIpam)
			pooledUuid := server.createZone("zone-pooled")
			Expect(storedRangeCount(pooledUuid)).To(Equal(1))

			_, err := server.DeleteZone(userCtx(adminUser), &svccore.UuidRequest{UUID: pooledUuid})
			Expect(err).ToNot(HaveOccurred())

			Expect(storedRangeCount(pooledUuid)).To(BeZero())
			_, err = server.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: newZone("zone-again")})
			Expect(err).ToNot(HaveOccurred())
		})
	})
	Context("pools of a zone", func() {
		var zoneUuid string
		var vipUuid string
		BeforeEach(func() {
			server = newTestServerWithIpam(instantiaters.NewNull(), dbIpam)
			zoneUuid = server.createZone("zone-1")
			registered, err := server.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{
				Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: zoneUuid},
			})
			Expect(err).ToNot(HaveOccurred())
			vipUuid = registered.GetVip().GetUUID()
		})
		readPools := func(zoneUuid string) []string {
			GinkgoHelper()
			read, err := server.ReadZone(userCtx(adminUser), &svccore.UuidRequest{UUID: zoneUuid})
			Expect(err).ToNot(HaveOccurred())
			names := make([]string, 0, len(read.GetZone().GetPools()))
			for _, pool := range read.GetZone().GetPools() {
				names = append(names, pool.GetName())
			}
			return names
		}
		rangePool := func(name, ipMin, ipMax string) *zone.IpPool {
			return &zone.IpPool{Name: name, IpRanges: []*zone.IpRange{{IpMin: ipMin, IpMax: ipMax}}}
		}

		It("should add a pool after the others while a pool holds an ip", func() {
			resp, err := server.AddPoolToZone(userCtx(adminUser), &svczone.AddPoolToZoneRequest{
				UUID: zoneUuid, Pool: rangePool("b", "10.1.0.1", "10.1.0.10"),
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetZone().GetPools()).To(HaveLen(2))
			Expect(readPools(zoneUuid)).To(Equal([]string{"default", "b"}))
		})
		It("should refuse to add a pool with an existing name", func() {
			_, err := server.AddPoolToZone(userCtx(adminUser), &svczone.AddPoolToZoneRequest{
				UUID: zoneUuid, Pool: rangePool("default", "10.1.0.1", "10.1.0.10"),
			})
			expectCode(err, connect.CodeAlreadyExists)
		})
		It("should refuse to add a pool overlapping another one", func() {
			_, err := server.AddPoolToZone(userCtx(adminUser), &svczone.AddPoolToZoneRequest{
				UUID: zoneUuid, Pool: rangePool("b", "10.0.0.100", "10.0.0.110"),
			})
			expectCode(err, connect.CodeInvalidArgument)
			Expect(readPools(zoneUuid)).To(Equal([]string{"default"}))
		})
		It("should keep both pools added at the same time", func() {
			var wg sync.WaitGroup
			for i, name := range []string{"p1", "p2"} {
				wg.Add(1)
				go func() {
					defer GinkgoRecover()
					defer wg.Done()
					ipMin := fmt.Sprintf("10.%d.0.1", i+1)
					ipMax := fmt.Sprintf("10.%d.0.10", i+1)
					_, err := server.AddPoolToZone(userCtx(adminUser), &svczone.AddPoolToZoneRequest{
						UUID: zoneUuid, Pool: rangePool(name, ipMin, ipMax),
					})
					Expect(err).ToNot(HaveOccurred())
				}()
			}
			wg.Wait()

			Expect(readPools(zoneUuid)).To(ConsistOf("default", "p1", "p2"))
		})
		It("should grow a pool holding an ip", func() {
			grown := newZone("zone-1").GetPools()[0]
			grown.IpRanges = []*zone.IpRange{{IpMin: "10.0.1.0", IpMax: "10.0.1.10"}}

			_, err := server.UpdatePoolOfZone(userCtx(adminUser), &svczone.UpdatePoolOfZoneRequest{UUID: zoneUuid, Pool: grown})

			Expect(err).ToNot(HaveOccurred())
			Expect(allocatedIps(zoneUuid)).To(Equal(map[string]string{"10.0.0.1": vipUuid}))
			read, err := server.ReadZone(userCtx(adminUser), &svccore.UuidRequest{UUID: zoneUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetZone().GetPools()[0].GetIpRanges()).To(HaveLen(1))
			Expect(read.GetZone().GetPools()[0].GetIpRanges()[0].GetIpMin()).To(Equal("10.0.1.0"))
		})
		It("should refuse to shrink a pool out of its allocated ip and keep the pool", func() {
			shrunk := rangePool("default", "10.0.0.100", "10.0.0.110")

			_, err := server.UpdatePoolOfZone(userCtx(adminUser), &svczone.UpdatePoolOfZoneRequest{UUID: zoneUuid, Pool: shrunk})

			expectCode(err, connect.CodeFailedPrecondition)
			read, err := server.ReadZone(userCtx(adminUser), &svccore.UuidRequest{UUID: zoneUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetZone().GetPools()[0].GetSubnets()).To(HaveLen(1))
			Expect(read.GetZone().GetPools()[0].GetIpRanges()).To(BeEmpty())
		})
		It("should answer not found for an update of an unknown pool", func() {
			_, err := server.UpdatePoolOfZone(userCtx(adminUser), &svczone.UpdatePoolOfZoneRequest{
				UUID: zoneUuid, Pool: rangePool("unknown", "10.1.0.1", "10.1.0.10"),
			})
			expectCode(err, connect.CodeNotFound)
		})
		It("should refuse to delete a pool holding an ip, so to rename it", func() {
			_, err := server.DeletePoolFromZone(userCtx(adminUser), &svczone.DeletePoolFromZoneRequest{UUID: zoneUuid, PoolName: "default"})

			expectCode(err, connect.CodeFailedPrecondition)
			Expect(readPools(zoneUuid)).To(Equal([]string{"default"}))
		})
		It("should delete a pool without ips", func() {
			_, err := server.DeleteVIP(userCtx(adminUser), &svccore.UuidRequest{UUID: vipUuid})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.DeletePoolFromZone(userCtx(adminUser), &svczone.DeletePoolFromZoneRequest{UUID: zoneUuid, PoolName: "default"})

			Expect(err).ToNot(HaveOccurred())
			Expect(readPools(zoneUuid)).To(BeEmpty())
		})
		It("should answer not found for the deletion of an unknown pool", func() {
			_, err := server.DeletePoolFromZone(userCtx(adminUser), &svczone.DeletePoolFromZoneRequest{UUID: zoneUuid, PoolName: "unknown"})
			expectCode(err, connect.CodeNotFound)
		})
		It("should refuse a pool change to a non admin", func() {
			_, err := server.AddPoolToZone(userCtx("bob"), &svczone.AddPoolToZoneRequest{
				UUID: zoneUuid, Pool: rangePool("b", "10.1.0.1", "10.1.0.10"),
			})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("should refuse a pool request without pool through the chain", func() {
			client := svczoneconnect.NewZoneClient(server.throughChain())
			_, err := client.AddPoolToZone(userCtx(adminUser), &svczone.AddPoolToZoneRequest{UUID: zoneUuid})
			expectCode(err, connect.CodeInvalidArgument)
		})

		Context("unmanaged zone", func() {
			var unmanagedUuid string
			var unmanagedVipUuid string
			var unmanagedInstUuid string
			BeforeEach(func() {
				resp, err := server.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: &zone.Zone{Name: "unmanaged"}})
				Expect(err).ToNot(HaveOccurred())
				unmanagedUuid = resp.GetZone().GetUUID()
				unmanagedVip := newVip("vip-unmanaged")
				unmanagedVip.Ip = "10.0.0.5"
				vipResp, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: unmanagedVip})
				Expect(err).ToNot(HaveOccurred())
				unmanagedVipUuid = vipResp.GetVip().GetUUID()
				instResp, err := server.CreateInstance(userCtx(adminUser), &svcinstance.CreateInstanceRequest{
					Instance: newInstance("inst-unmanaged", unmanagedUuid, unmanagedVipUuid),
				})
				Expect(err).ToNot(HaveOccurred())
				unmanagedInstUuid = instResp.GetInstance().GetUUID()
			})

			It("should record the ips of the vips of its instances with its first pool", func() {
				_, err := server.AddPoolToZone(userCtx(adminUser), &svczone.AddPoolToZoneRequest{
					UUID: unmanagedUuid, Pool: &zone.IpPool{Name: "a", Subnets: []*zone.Subnet{{Ip: "10.0.0.0", PrefixLength: 24}}},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(allocatedIps(unmanagedUuid)).To(Equal(map[string]string{"10.0.0.5": unmanagedVipUuid}))
			})
			It("should refuse a first pool without the ips of its vips and stay without pools", func() {
				_, err := server.AddPoolToZone(userCtx(adminUser), &svczone.AddPoolToZoneRequest{
					UUID: unmanagedUuid, Pool: &zone.IpPool{Name: "a", Subnets: []*zone.Subnet{{Ip: "10.1.0.0", PrefixLength: 24}}},
				})

				expectCode(err, connect.CodeFailedPrecondition)
				Expect(err).To(MatchError(ContainSubstring(unmanagedVipUuid)))
				Expect(readPools(unmanagedUuid)).To(BeEmpty())
				Expect(allocatedIps(unmanagedUuid)).To(BeEmpty())
			})
			It("should keep the ip of a vip in its other zones", func() {
				_, err := server.CreateInstance(userCtx(adminUser), &svcinstance.CreateInstanceRequest{
					Instance: newInstance("inst-managed", zoneUuid, unmanagedVipUuid),
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(allocatedIps(zoneUuid)).To(HaveKey("10.0.0.5"))

				_, err = server.AddPoolToZone(userCtx(adminUser), &svczone.AddPoolToZoneRequest{
					UUID: unmanagedUuid, Pool: &zone.IpPool{Name: "a", Subnets: []*zone.Subnet{{Ip: "10.0.0.0", PrefixLength: 24}}},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(allocatedIps(zoneUuid)).To(HaveKeyWithValue("10.0.0.5", unmanagedVipUuid))
				Expect(allocatedIps(unmanagedUuid)).To(HaveKeyWithValue("10.0.0.5", unmanagedVipUuid))
			})
			It("should keep the ip of a vip in its other zones when the first pool is refused", func() {
				_, err := server.CreateInstance(userCtx(adminUser), &svcinstance.CreateInstanceRequest{
					Instance: newInstance("inst-managed", zoneUuid, unmanagedVipUuid),
				})
				Expect(err).ToNot(HaveOccurred())
				outside := newVip("vip-outside")
				outside.Ip = "10.1.0.5"
				outsideResp, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: outside})
				Expect(err).ToNot(HaveOccurred())
				// after the vip recorded first, its ip has to be released again
				_, err = server.AddVipToInstance(userCtx(adminUser), &svcinstance.AddVipToInstanceRequest{
					UUID: unmanagedInstUuid, VipUuid: outsideResp.GetVip().GetUUID(),
				})
				Expect(err).ToNot(HaveOccurred())

				_, err = server.AddPoolToZone(userCtx(adminUser), &svczone.AddPoolToZoneRequest{
					UUID: unmanagedUuid, Pool: &zone.IpPool{Name: "a", Subnets: []*zone.Subnet{{Ip: "10.0.0.0", PrefixLength: 24}}},
				})

				expectCode(err, connect.CodeFailedPrecondition)
				Expect(allocatedIps(unmanagedUuid)).To(BeEmpty())
				Expect(allocatedIps(zoneUuid)).To(HaveKeyWithValue("10.0.0.5", unmanagedVipUuid))
			})
		})
	})
})
