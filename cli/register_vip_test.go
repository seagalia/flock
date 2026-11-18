package cli

import (
	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
)

var _ = Describe("register-vip", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcvipconnect.VIPRegisterVipProcedure, reply(&svcvip.VIPResponse{Vip: &vip.VIP{Name: "public", Ip: "10.0.0.1"}}))
	})

	It("registers a vip on an instance", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceReadInstanceProcedure, reply(&svcinstance.InstanceResponse{Instance: &instance.Instance{UUID: resourceUuid}}))

		out, err := runner.run("register-vip", "--name", "public", "--instance", resourceUuid)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("Vip successfully registered with ip 10.0.0.1."))
		sent := lastRequest[*svcvip.RegisterVipRequest](runner.conn, svcvipconnect.VIPRegisterVipProcedure)
		Expect(sent.GetName()).To(Equal("public"))
		Expect(sent.GetInstanceUuid()).To(Equal(resourceUuid))
		Expect(sent.GetZoneMetadata()).To(BeNil())
	})

	It("registers a vip in a zone found by its name", func() {
		runner.conn.Handle(svczoneconnect.ZoneListZoneProcedure, reply(&svczone.ListZoneResponse{Zones: []*zone.Zone{{UUID: zoneUuid, Name: "production"}}}))

		_, err := runner.run("register-vip", "--name", "public", "--zone", "production")

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svcvip.RegisterVipRequest](runner.conn, svcvipconnect.VIPRegisterVipProcedure)
		Expect(sent.GetZoneUuid()).To(Equal(zoneUuid))
	})

	It("registers a vip in a zone selected by metadata", func() {
		_, err := runner.run("register-vip", "--name", "public", "-m", "site=paris", "-m", "tier=front")

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svcvip.RegisterVipRequest](runner.conn, svcvipconnect.VIPRegisterVipProcedure)
		Expect(sent.GetZoneMetadata().GetMetadata()).To(Equal(map[string]string{"site": "paris", "tier": "front"}))
	})

	It("takes the ip in the pool named with --pool", func() {
		runner.conn.Handle(svczoneconnect.ZoneListZoneProcedure, reply(&svczone.ListZoneResponse{Zones: []*zone.Zone{{UUID: zoneUuid, Name: "z1"}}}))

		_, err := runner.run("register-vip", "--name", "public", "-z", "z1", "--pool", "ns-a")

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svcvip.RegisterVipRequest](runner.conn, svcvipconnect.VIPRegisterVipProcedure)
		Expect(sent.GetPoolName()).To(Equal("ns-a"))
		Expect(sent.GetZoneUuid()).To(Equal(zoneUuid))
	})

	It("takes the ip in the pool selected by metadata on the zone of an instance", func() {
		runner.conn.Handle(svcinstanceconnect.InstanceReadInstanceProcedure, reply(&svcinstance.InstanceResponse{Instance: &instance.Instance{UUID: resourceUuid}}))

		_, err := runner.run("register-vip", "--name", "public", "-i", resourceUuid, "--pool-metadata", "ns=a")

		Expect(err).NotTo(HaveOccurred())
		sent := lastRequest[*svcvip.RegisterVipRequest](runner.conn, svcvipconnect.VIPRegisterVipProcedure)
		Expect(sent.GetPoolMetadata().GetMetadata()).To(Equal(map[string]string{"ns": "a"}))
		Expect(sent.GetInstanceUuid()).To(Equal(resourceUuid))
	})

	It("sends no pool without --pool nor --pool-metadata", func() {
		_, err := runner.run("register-vip", "--name", "public", "-m", "site=paris")

		Expect(err).NotTo(HaveOccurred())
		Expect(lastRequest[*svcvip.RegisterVipRequest](runner.conn, svcvipconnect.VIPRegisterVipProcedure).GetPool()).To(BeNil())
	})

	It("needs where to take the ip", func() {
		_, err := runner.run("register-vip", "--name", "public")

		Expect(err).To(MatchError(ContainSubstring("one of --instance, --zone or --zone-metadata is required")))
		Expect(runner.conn.Requests(svcvipconnect.VIPRegisterVipProcedure)).To(BeEmpty())
	})

	It("refuses two places to take the ip from", func() {
		_, err := runner.run("register-vip", "--name", "public", "-z", "z1", "-m", "site=paris")

		Expect(err).To(MatchError(ContainSubstring("can't be used together")))
		Expect(runner.conn.Requests(svcvipconnect.VIPRegisterVipProcedure)).To(BeEmpty())
	})

	It("refuses two pools", func() {
		_, err := runner.run("register-vip", "--name", "public", "-m", "site=paris", "--pool", "a", "--pool-metadata", "ns=a")

		Expect(err).To(MatchError(ContainSubstring("can't be used together")))
	})

	It("returns the server error", func() {
		runner.conn.Handle(svcvipconnect.VIPRegisterVipProcedure, failWith(connect.NewError(connect.CodeFailedPrecondition, "zone is full")))

		_, err := runner.run("register-vip", "--name", "public", "-m", "site=paris")

		Expect(err).To(MatchError(ContainSubstring("zone is full")))
	})
})
