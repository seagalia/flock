package serves_test

import (
	"context"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/permission/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("VIP", func() {
	var server *testServer
	vipType := helpers.TypeUrl[*vip.VIP]()
	BeforeEach(func() {
		server = newTestServer(instantiaters.NewNull())
	})
	createVs := func(name string, port uint32) string {
		backendResp, err := server.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-" + name)})
		Expect(err).ToNot(HaveOccurred())
		vsResp, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: newVs(name, port, backendResp.GetBackend().GetUUID())})
		Expect(err).ToNot(HaveOccurred())
		return vsResp.GetVs().GetUUID()
	}

	Context("CreateVIP", func() {
		It("should create vip as admin with default ipv4 prefix length", func() {
			client := svcvipconnect.NewVIPClient(server.throughChain())
			resp, err := client.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1")})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetVip().GetPrefixLength()).To(Equal(int32(32)))
		})
		It("should default prefix length to 128 for ipv6", func() {
			ipv6Vip := newVip("vip-1")
			ipv6Vip.Ip = "2001:db8::1"

			client := svcvipconnect.NewVIPClient(server.throughChain())
			resp, err := client.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: ipv6Vip})

			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetVip().GetPrefixLength()).To(Equal(int32(128)))
		})
		It("should be refused to non admin", func() {
			_, err := server.CreateVIP(userCtx("bob"), &svcvip.CreateVIPRequest{Vip: newVip("vip-1")})
			expectCode(err, connect.CodePermissionDenied)
		})
		It("should refuse invalid ip", func() {
			invalid := newVip("vip-1")
			invalid.Ip = "not-an-ip"

			client := svcvipconnect.NewVIPClient(server.throughChain())
			_, err := client.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: invalid})

			expectCode(err, connect.CodeInvalidArgument)
		})
		It("should refuse virtual servers sharing the same port", func() {
			vs1 := createVs("vs-1", 80)
			vs2 := createVs("vs-2", 80)

			_, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1", vs1, vs2)})

			Expect(err).To(MatchError(ContainSubstring("port tcp/80 is used by virtual servers")))
		})
		It("should accept a tcp and an udp virtual server on the same port", func() {
			tcpVs := createVs("vs-tcp", 53)
			backendResp, err := server.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-udp")})
			Expect(err).ToNot(HaveOccurred())
			udp := &vs.VirtualServer{Name: "vs-udp", Port: 53, RouteConfig: &vs.VirtualServer_UdpRouteConfig{
				UdpRouteConfig: &vs.UdpRouteConfig{BackendUuid: backendResp.GetBackend().GetUUID()},
			}}
			udpResp, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: udp})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1", tcpVs, udpResp.GetVs().GetUUID())})

			Expect(err).ToNot(HaveOccurred())
		})
		It("should accept tls virtual servers with distinct server names on one port", func() {
			passthroughVs := func(name, serverName string) string {
				backendResp, err := server.CreateBackend(userCtx("bob"), &svcbackend.CreateBackendRequest{Backend: newBackend("backend-" + name)})
				Expect(err).ToNot(HaveOccurred())
				virtServ := &vs.VirtualServer{Name: name, Port: 443, RouteConfig: &vs.VirtualServer_TlsTcpRoutesConfig{
					TlsTcpRoutesConfig: &vs.TlsTcpRoutesConfig{Routes: []*vs.TlsTcpRouteConfig{{
						BackendUuid: backendResp.GetBackend().GetUUID(),
						Mode:        &vs.TlsTcpRouteConfig_Passthrough{Passthrough: &vs.TlsPassthrough{ServerNames: []string{serverName}}},
					}}},
				}}
				vsResp, err := server.CreateVS(userCtx("bob"), &svcvs.CreateVSRequest{Vs: virtServ})
				Expect(err).ToNot(HaveOccurred())
				return vsResp.GetVs().GetUUID()
			}

			vip1 := newVip("vip-1", passthroughVs("git", "git.local"), passthroughVs("db", "db.local"))
			_, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: vip1})

			Expect(err).ToNot(HaveOccurred())
		})
		It("should accept virtual servers on different ports", func() {
			vs1 := createVs("vs-1", 80)
			vs2 := createVs("vs-2", 443)

			_, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1", vs1, vs2)})

			Expect(err).ToNot(HaveOccurred())
		})
		It("should refuse unknown virtual server", func() {
			_, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1", "unknown")})
			expectCode(err, connect.CodeInternal)
		})
	})

	Context("existing vip", func() {
		var vipUuid string
		BeforeEach(func() {
			client := svcvipconnect.NewVIPClient(server.throughChain())
			resp, err := client.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1")})
			Expect(err).ToNot(HaveOccurred())
			vipUuid = resp.GetVip().GetUUID()
			Expect(server.perm.AddPermission(context.Background(), vipType, vipUuid, permission.Role_OWNER, permission.MemberType_USER, "bob")).To(Succeed())
		})

		It("should be readable by owner", func() {
			resp, err := server.ReadVIP(userCtx("bob"), &svccore.UuidRequest{UUID: vipUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetVip().GetIp()).To(Equal("10.0.0.1"))
		})
		It("should let owner attach virtual servers", func() {
			vsUuid := createVs("vs-1", 80)
			updated := newVip("vip-1", vsUuid)
			updated.PrefixLength = 32

			_, err := server.UpdateVIP(userCtx("bob"), &svcvip.UpdateVIPRequest{UUID: vipUuid, Vip: updated})
			Expect(err).ToNot(HaveOccurred())

			read, err := server.ReadVIP(userCtx("bob"), &svccore.UuidRequest{UUID: vipUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetVip().GetVirtualServersUuid()).To(Equal([]string{vsUuid}))
		})
		It("should let owner update without a prefix length, the default one is kept", func() {
			client := svcvipconnect.NewVIPClient(server.throughChain())
			_, err := client.UpdateVIP(userCtx("bob"), &svcvip.UpdateVIPRequest{UUID: vipUuid, Vip: newVip("vip-1")})
			Expect(err).ToNot(HaveOccurred())

			read, err := server.ReadVIP(userCtx("bob"), &svccore.UuidRequest{UUID: vipUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(read.GetVip().GetPrefixLength()).To(Equal(int32(32)))
		})
		It("should refuse non admin to change ip", func() {
			updated := newVip("vip-1")
			updated.Ip = "10.0.0.2"
			updated.PrefixLength = 32

			_, err := server.UpdateVIP(userCtx("bob"), &svcvip.UpdateVIPRequest{UUID: vipUuid, Vip: updated})

			expectCode(err, connect.CodePermissionDenied)
		})
		It("should refuse non admin to change prefix length", func() {
			updated := newVip("vip-1")
			updated.PrefixLength = 24

			_, err := server.UpdateVIP(userCtx("bob"), &svcvip.UpdateVIPRequest{UUID: vipUuid, Vip: updated})

			expectCode(err, connect.CodePermissionDenied)
		})
		It("should let admin change ip", func() {
			updated := newVip("vip-1")
			updated.Ip = "10.0.0.2"

			_, err := server.UpdateVIP(userCtx(adminUser), &svcvip.UpdateVIPRequest{UUID: vipUuid, Vip: updated})

			Expect(err).ToNot(HaveOccurred())
		})
		It("should list vips of user", func() {
			resp, err := server.ListVIP(userCtx("bob"), &svcvip.ListVIPRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetVip()).To(HaveLen(1))

			resp, err = server.ListVIP(userCtx("alice"), &svcvip.ListVIPRequest{})
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.GetVip()).To(BeEmpty())
		})
		It("should be deleted only by admin", func() {
			_, err := server.DeleteVIP(userCtx("bob"), &svccore.UuidRequest{UUID: vipUuid})
			expectCode(err, connect.CodePermissionDenied)

			_, err = server.DeleteVIP(userCtx(adminUser), &svccore.UuidRequest{UUID: vipUuid})
			Expect(err).ToNot(HaveOccurred())
			Expect(dbStoreMan.VIPs().Exists(context.Background(), vipUuid)).To(BeFalse())
		})
		It("should refuse to delete vip used by an instance", func() {
			zoneUuid := server.createZone("zone-1")
			_, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid, vipUuid)})
			Expect(err).ToNot(HaveOccurred())

			_, err = server.DeleteVIP(userCtx(adminUser), &svccore.UuidRequest{UUID: vipUuid})

			expectCode(err, connect.CodeFailedPrecondition)
		})
	})

	Context("with the ipam", func() {
		var zoneUuid string
		BeforeEach(func() {
			server = newTestServerWithIpam(instantiaters.NewNull(), dbIpam)
			zoneUuid = server.createZone("zone-1")
		})

		It("should refuse to change the ip of a vip", func() {
			resp, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1")})
			Expect(err).ToNot(HaveOccurred())
			updated := newVip("vip-1")
			updated.Ip = "10.0.0.2"

			_, err = server.UpdateVIP(userCtx(adminUser), &svcvip.UpdateVIPRequest{UUID: resp.GetVip().GetUUID(), Vip: updated})

			expectCode(err, connect.CodeInvalidArgument)
		})

		Context("RegisterVip", func() {
			var instUuid string
			BeforeEach(func() {
				resp, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-1", zoneUuid)})
				Expect(err).ToNot(HaveOccurred())
				instUuid = resp.GetInstance().GetUUID()
			})

			It("should take an ip in the zone of the instance and add the vip to it", func() {
				resp, err := server.RegisterVip(userCtx("bob"), &svcvip.RegisterVipRequest{Name: "vip-1", Location: &svcvip.RegisterVipRequest_InstanceUuid{InstanceUuid: instUuid}})
				Expect(err).ToNot(HaveOccurred())

				registered := resp.GetVip()
				Expect(registered.GetIp()).To(Equal("10.0.0.1"), "first ip of the subnet after the network address")
				Expect(registered.GetPrefixLength()).To(Equal(int32(32)))
				Expect(allocatedIps(zoneUuid)).To(Equal(map[string]string{"10.0.0.1": registered.GetUUID()}))
				stored, err := dbStoreMan.Instances().Read(context.Background(), instUuid)
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetVipsUuid()).To(Equal([]string{registered.GetUUID()}))
				Expect(server.perm.HasPermission(context.Background(), vipType, registered.GetUUID(), permission.Role_OWNER, "bob", nil)).To(BeTrue())
			})
			It("should give the next ip to the next vip", func() {
				_, err := server.RegisterVip(userCtx("bob"), &svcvip.RegisterVipRequest{Name: "vip-1", Location: &svcvip.RegisterVipRequest_InstanceUuid{InstanceUuid: instUuid}})
				Expect(err).ToNot(HaveOccurred())

				resp, err := server.RegisterVip(userCtx("bob"), &svcvip.RegisterVipRequest{Name: "vip-2", Location: &svcvip.RegisterVipRequest_InstanceUuid{InstanceUuid: instUuid}})

				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetVip().GetIp()).To(Equal("10.0.0.2"))
			})
			It("should keep the ip of a vip registered in a zone until the vip is deleted", func() {
				resp, err := server.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{
					Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: zoneUuid},
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(allocatedIps(zoneUuid)).To(HaveLen(1))
				stored, err := dbStoreMan.Instances().Read(context.Background(), instUuid)
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetVipsUuid()).To(BeEmpty(), "a vip registered in a zone is on no instance")

				_, err = server.DeleteVIP(userCtx(adminUser), &svccore.UuidRequest{UUID: resp.GetVip().GetUUID()})
				Expect(err).ToNot(HaveOccurred())
				Expect(allocatedIps(zoneUuid)).To(BeEmpty())
			})
			It("should find the zone by its metadata", func() {
				other := newZone("zone-2")
				other.Pools[0].Subnets = []*zone.Subnet{{Ip: "10.1.0.0", PrefixLength: 24}}
				other.Metadata = map[string]string{"site": "lyon"}
				_, err := server.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: other})
				Expect(err).ToNot(HaveOccurred())

				resp, err := server.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{
					Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneMetadata{ZoneMetadata: &svcvip.MetadataSelector{Metadata: map[string]string{"site": "lyon"}}},
				})

				Expect(err).ToNot(HaveOccurred())
				Expect(resp.GetVip().GetIp()).To(Equal("10.1.0.1"))
			})
			It("should refuse a zone the user cannot read", func() {
				_, err := server.RegisterVip(userCtx("bob"), &svcvip.RegisterVipRequest{
					Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: zoneUuid},
				})
				expectCode(err, connect.CodePermissionDenied)

				Expect(server.perm.AddPermission(context.Background(), helpers.TypeUrl[*zone.Zone](), zoneUuid, permission.Role_READER, permission.MemberType_USER, "bob")).To(Succeed())
				_, err = server.RegisterVip(userCtx("bob"), &svcvip.RegisterVipRequest{
					Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: zoneUuid},
				})
				Expect(err).ToNot(HaveOccurred())
			})
			It("should refuse an instance the user cannot write", func() {
				_, err := server.RegisterVip(userCtx("alice"), &svcvip.RegisterVipRequest{Name: "vip-1", Location: &svcvip.RegisterVipRequest_InstanceUuid{InstanceUuid: instUuid}})
				expectCode(err, connect.CodePermissionDenied)
				Expect(allocatedIps(zoneUuid)).To(BeEmpty())
			})
			It("should refuse an existing vip name", func() {
				_, err := server.CreateVIP(userCtx(adminUser), &svcvip.CreateVIPRequest{Vip: newVip("vip-1")})
				Expect(err).ToNot(HaveOccurred())

				_, err = server.RegisterVip(userCtx("bob"), &svcvip.RegisterVipRequest{Name: "vip-1", Location: &svcvip.RegisterVipRequest_InstanceUuid{InstanceUuid: instUuid}})

				expectCode(err, connect.CodeAlreadyExists)
			})
			It("should refuse a zone without ip to allocate", func() {
				public, err := server.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: &zone.Zone{Name: "public"}})
				Expect(err).ToNot(HaveOccurred())

				_, err = server.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{
					Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: public.GetZone().GetUUID()},
				})

				expectCode(err, connect.CodeFailedPrecondition)
			})
			It("should refuse a zone without public pool", func() {
				private := newZone("private")
				private.Pools[0].Privacy = zone.IpPool_PRIVATE
				private.Pools[0].Subnets = []*zone.Subnet{{Ip: "10.2.0.0", PrefixLength: 24}}
				created, err := server.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: private})
				Expect(err).ToNot(HaveOccurred())

				_, err = server.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{
					Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: created.GetZone().GetUUID()},
				})

				expectCode(err, connect.CodeFailedPrecondition)
				Expect(err).To(MatchError(ContainSubstring("no public pool")))
				Expect(allocatedIps(created.GetZone().GetUUID())).To(BeEmpty())
			})
			Context("with a pool selector", func() {
				var pooledUuid string
				// register takes a request holding only its pool, nil for none
				register := func(withPool *svcvip.RegisterVipRequest, zoneUuid string) (*svcvip.VIPResponse, error) {
					req := &svcvip.RegisterVipRequest{Name: "vip-pool", Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: zoneUuid}}
					req.Pool = withPool.GetPool()
					return server.RegisterVip(userCtx(adminUser), req)
				}
				poolNamed := func(name string) *svcvip.RegisterVipRequest {
					return &svcvip.RegisterVipRequest{Pool: &svcvip.RegisterVipRequest_PoolName{PoolName: name}}
				}
				createZoneWith := func(name string, pools ...*zone.IpPool) string {
					GinkgoHelper()
					resp, err := server.CreateZone(userCtx(adminUser), &svczone.CreateZoneRequest{Zone: &zone.Zone{Name: name, Pools: pools}})
					Expect(err).ToNot(HaveOccurred())
					return resp.GetZone().GetUUID()
				}
				subnetPool := func(name string, privacy zone.IpPool_Privacy, ip string, metadata map[string]string) *zone.IpPool {
					return &zone.IpPool{
						Name: name, Privacy: privacy, Metadata: metadata,
						Subnets: []*zone.Subnet{{Ip: ip, PrefixLength: 24}},
					}
				}
				BeforeEach(func() {
					pooledUuid = createZoneWith("pooled",
						subnetPool("default", zone.IpPool_PUBLIC, "10.10.0.0", nil),
						subnetPool("ns-a", zone.IpPool_PRIVATE, "10.10.1.0", map[string]string{"ns": "a"}),
					)
				})

				It("should use the default pool without selector", func() {
					resp, err := register(nil, pooledUuid)
					Expect(err).ToNot(HaveOccurred())
					Expect(resp.GetVip().GetIp()).To(Equal("10.10.0.1"))
				})
				It("should use the pool of the given name", func() {
					resp, err := register(poolNamed("ns-a"), pooledUuid)
					Expect(err).ToNot(HaveOccurred())
					Expect(resp.GetVip().GetIp()).To(Equal("10.10.1.1"))
				})
				It("should use the first pool having the metadata", func() {
					resp, err := register(&svcvip.RegisterVipRequest{Pool: &svcvip.RegisterVipRequest_PoolMetadata{PoolMetadata: &svcvip.MetadataSelector{Metadata: map[string]string{"ns": "a"}}}}, pooledUuid)
					Expect(err).ToNot(HaveOccurred())
					Expect(resp.GetVip().GetIp()).To(Equal("10.10.1.1"))
				})
				It("should refuse an unknown pool", func() {
					_, err := register(poolNamed("nope"), pooledUuid)
					expectCode(err, connect.CodeNotFound)
				})
				It("should refuse an empty pool metadata", func() {
					client := svcvipconnect.NewVIPClient(server.throughChain())

					_, err := client.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{
						Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: pooledUuid}, Pool: &svcvip.RegisterVipRequest_PoolMetadata{PoolMetadata: &svcvip.MetadataSelector{}},
					})

					expectCode(err, connect.CodeInvalidArgument)
				})
				It("should refuse a zone with private pools only and no selector", func() {
					privateUuid := createZoneWith("private-only", subnetPool("ns-a", zone.IpPool_PRIVATE, "10.11.0.0", nil))
					_, err := register(nil, privateUuid)
					expectCode(err, connect.CodeFailedPrecondition)
				})
				It("should tell a zone without pools has no pool, with or without selector", func() {
					emptyUuid := createZoneWith("empty")
					_, err := register(nil, emptyUuid)
					expectCode(err, connect.CodeFailedPrecondition)

					_, err = register(poolNamed("ns-a"), emptyUuid)
					expectCode(err, connect.CodeFailedPrecondition)
					Expect(err).To(MatchError(ContainSubstring("has no pools")))
				})
				It("should choose the pool in the zone of the instance", func() {
					inst, err := server.CreateInstance(userCtx("bob"), &svcinstance.CreateInstanceRequest{Instance: newInstance("inst-pooled", pooledUuid)})
					Expect(err).ToNot(HaveOccurred())

					resp, err := server.RegisterVip(userCtx("bob"), &svcvip.RegisterVipRequest{
						Name: "vip-pool", Location: &svcvip.RegisterVipRequest_InstanceUuid{InstanceUuid: inst.GetInstance().GetUUID()}, Pool: &svcvip.RegisterVipRequest_PoolName{PoolName: "ns-a"},
					})

					Expect(err).ToNot(HaveOccurred())
					Expect(resp.GetVip().GetIp()).To(Equal("10.10.1.1"))
				})
			})
			It("should need an instance, a zone or zone metadata", func() {
				client := svcvipconnect.NewVIPClient(server.throughChain())

				_, err := client.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{Name: "vip-1"})
				expectCode(err, connect.CodeInvalidArgument)

				_, err = client.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{
					Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneMetadata{ZoneMetadata: &svcvip.MetadataSelector{}},
				})
				expectCode(err, connect.CodeInvalidArgument)

				_, err = client.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{
					Name: "vip-1", Location: &svcvip.RegisterVipRequest_ZoneUuid{ZoneUuid: ""},
				})
				expectCode(err, connect.CodeInvalidArgument)
			})
		})
	})

	It("should refuse to register a vip without ipam", func() {
		_, err := server.RegisterVip(userCtx(adminUser), &svcvip.RegisterVipRequest{Name: "vip-1", Location: &svcvip.RegisterVipRequest_InstanceUuid{InstanceUuid: "any"}})
		expectCode(err, connect.CodeFailedPrecondition)
	})
})
