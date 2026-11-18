package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
	"github.com/seagalia/flock/sdk/flock/services/permission/v1/svcpermconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ = Describe("autoprovision lock", func() {
	const explanation = "Managed automatically (metadata flock.autoprovision). Change it where it was provisioned."
	locked := map[string]string{"flock.autoprovision": "true"}
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
	})

	It("tells a locked resource from the others", func() {
		Expect(isLocked(locked)).To(BeTrue())
		Expect(isLocked(map[string]string{helpers.MetadataLockKey: "false"})).To(BeFalse())
		Expect(isLocked(nil)).To(BeFalse())
		Expect(refuseLocked("vs", "web", locked)).To(MatchError("vs web is locked. " + explanation))
		Expect(refuseLockAdded(nil, locked)).To(HaveOccurred())
		Expect(refuseLockAdded(map[string]string{}, locked)).To(HaveOccurred())
		Expect(refuseLockAdded(locked, locked)).To(Succeed())
		Expect(refuseLockAdded(nil, map[string]string{"a": "b"})).To(Succeed())
	})

	It("refuses update-vs of a locked virtual server without any write", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerReadVSProcedure,
			reply(&svcvs.VSResponse{Vs: &vs.VirtualServer{UUID: resourceUuid, Name: "web-https", Metadata: locked}}))

		_, err := runner.run("update-vs", "--force", "--file", writeTempFile("vs.yml", "name: renamed"), resourceUuid)

		Expect(err).To(MatchError("vs web-https is locked. " + explanation))
		Expect(runner.conn.Requests(svcvsconnect.VirtualServerUpdateVSProcedure)).To(BeEmpty())
	})

	It("refuses update-vs that adds the lock key", func() {
		runner.conn.Handle(svcvsconnect.VirtualServerReadVSProcedure,
			reply(&svcvs.VSResponse{Vs: &vs.VirtualServer{UUID: resourceUuid, Name: "web"}}))
		file := writeTempFile("vs.yml", "name: web\nmetadata:\n  flock.autoprovision: \"true\"\n")

		_, err := runner.run("update-vs", "--force", "--file", file, resourceUuid)

		Expect(err).To(MatchError(ContainSubstring("flock.autoprovision")))
		Expect(runner.conn.Requests(svcvsconnect.VirtualServerUpdateVSProcedure)).To(BeEmpty())
	})

	It("refuses create-vs of a manifest carrying the key", func() {
		file := writeTempFile("vs.yml", "name: web\nmetadata:\n  flock.autoprovision: \"true\"\n")

		_, err := runner.run("create-vs", "--force", "--file", file)

		Expect(err).To(MatchError(ContainSubstring("flock.autoprovision")))
		Expect(runner.conn.Requests(svcvsconnect.VirtualServerCreateVSProcedure)).To(BeEmpty())
	})

	It("refuses delete-backend of a locked backend", func() {
		runner.conn.Handle(svcbackendconnect.BackendReadBackendProcedure,
			reply(&svcbackend.BackendResponse{Backend: &backend.Backend{UUID: resourceUuid, Name: "api", Metadata: locked}}))

		_, err := runner.run("delete-backend", "--force", resourceUuid)

		Expect(err).To(MatchError("backend api is locked. " + explanation))
		Expect(runner.conn.Requests(svcbackendconnect.BackendDeleteBackendProcedure)).To(BeEmpty())
	})

	Describe("forced delete", func() {
		provisioned := map[string]string{
			"flock.autoprovision":    "true",
			"flock.provisioner.acme": "true",
			"flock.provision.by":     "agent-1",
		}

		BeforeEach(func() {
			runner.conn.Handle(svcbackendconnect.BackendReadBackendProcedure,
				reply(&svcbackend.BackendResponse{Backend: &backend.Backend{UUID: resourceUuid, Name: "api", Metadata: provisioned}}))
			runner.conn.Handle(svcbackendconnect.BackendDeleteBackendProcedure, reply(&emptypb.Empty{}))
		})

		It("deletes a locked backend for an admin with --force-locked and warns about the provisioner", func() {
			runner.conn.Handle(svcloginconnect.LoginMeProcedure, reply(&svclogin.MeResponse{Username: "admin", IsAdmin: true}))

			output, err := runner.run("delete-backend", "--force", "--force-locked", resourceUuid)

			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("provisioner acme"))
			Expect(output).To(ContainSubstring("agent-1"))
			Expect(runner.conn.Requests(svcbackendconnect.BackendDeleteBackendProcedure)).To(HaveLen(1))
		})

		It("refuses --force-locked to a user who is not an admin", func() {
			runner.conn.Handle(svcloginconnect.LoginMeProcedure, reply(&svclogin.MeResponse{Username: "alice"}))

			_, err := runner.run("delete-backend", "--force", "--force-locked", resourceUuid)

			Expect(err).To(MatchError(ContainSubstring("only an admin")))
			Expect(runner.conn.Requests(svcbackendconnect.BackendDeleteBackendProcedure)).To(BeEmpty())
		})

		It("points an admin at --force-locked when refusing", func() {
			runner.conn.Handle(svcloginconnect.LoginMeProcedure, reply(&svclogin.MeResponse{Username: "admin", IsAdmin: true}))

			_, err := runner.run("delete-backend", "--force", resourceUuid)

			Expect(err).To(MatchError(ContainSubstring("--force-locked")))
			Expect(runner.conn.Requests(svcbackendconnect.BackendDeleteBackendProcedure)).To(BeEmpty())
		})

		It("never points a user who is not an admin at --force-locked", func() {
			runner.conn.Handle(svcloginconnect.LoginMeProcedure, reply(&svclogin.MeResponse{Username: "alice"}))

			_, err := runner.run("delete-backend", "--force", resourceUuid)

			Expect(err).To(MatchError("backend api is locked. " + explanation))
		})

		It("asks nothing of the controller about the user for a resource that is not locked", func() {
			runner.conn.Handle(svcbackendconnect.BackendReadBackendProcedure,
				reply(&svcbackend.BackendResponse{Backend: &backend.Backend{UUID: resourceUuid, Name: "api"}}))

			_, err := runner.run("delete-backend", "--force", "--force-locked", resourceUuid)

			Expect(err).NotTo(HaveOccurred())
			Expect(runner.conn.Requests(svcloginconnect.LoginMeProcedure)).To(BeEmpty())
		})

		It("is offered by the delete of every resource", func() {
			for _, command := range []string{
				"delete-backend", "delete-certificate", "delete-instance", "delete-secret", "delete-vip", "delete-vs",
				"delete-zone",
			} {
				// no resource is served, the command fails after parsing its flags
				_, err := runner.run(command, "--force", "--force-locked", "missing")
				Expect(err).NotTo(MatchError(ContainSubstring("unknown flag")), command)
			}
		})
	})

	It("still adds a permission on a locked resource", func() {
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, reply(&svcbackend.ListBackendResponse{
			Backends: []*backend.Backend{{UUID: resourceUuid, Name: "api", Metadata: locked}},
		}))
		runner.conn.Handle(svcpermconnect.PermissionAddPermissionProcedure, reply(&emptypb.Empty{}))

		_, err := runner.run("add-permission", "-r", "backend", "--member-type", "user", "--member-name", "alice", "api")

		Expect(err).NotTo(HaveOccurred())
		Expect(runner.conn.Requests(svcpermconnect.PermissionAddPermissionProcedure)).To(HaveLen(1))
	})

	Describe("pools", func() {
		poolFile := func() string { return writeTempFile("pool.yml", "name: main") }

		stubZone := func(zoneMetadata, poolMetadata map[string]string) {
			runner.conn.Handle(svczoneconnect.ZoneReadZoneProcedure, reply(&svczone.ZoneResponse{Zone: &zone.Zone{
				UUID: resourceUuid, Name: "paris", Metadata: zoneMetadata,
				Pools: []*zone.IpPool{{Name: "main", Metadata: poolMetadata}},
			}}))
			runner.conn.Handle(svczoneconnect.ZoneUpdatePoolOfZoneProcedure, reply(&svczone.ZoneResponse{}))
			runner.conn.Handle(svczoneconnect.ZoneDeletePoolFromZoneProcedure, reply(&svczone.ZoneResponse{}))
			runner.conn.Handle(svczoneconnect.ZoneAddPoolToZoneProcedure, reply(&svczone.ZoneResponse{}))
		}

		It("refuses update-pool-of-zone on a locked pool of an unlocked zone", func() {
			stubZone(nil, locked)

			_, err := runner.run("update-pool-of-zone", "--force", "--file", poolFile(), resourceUuid)

			Expect(err).To(MatchError("pool main is locked. " + explanation))
			Expect(runner.conn.Requests(svczoneconnect.ZoneUpdatePoolOfZoneProcedure)).To(BeEmpty())
		})

		It("updates an unlocked pool of an unlocked zone", func() {
			stubZone(nil, nil)

			_, err := runner.run("update-pool-of-zone", "--force", "--file", poolFile(), resourceUuid)

			Expect(err).NotTo(HaveOccurred())
			Expect(runner.conn.Requests(svczoneconnect.ZoneUpdatePoolOfZoneProcedure)).To(HaveLen(1))
		})

		It("refuses every pool command on a locked zone", func() {
			stubZone(locked, nil)

			for _, args := range [][]string{
				{"update-pool-of-zone", "--force", "--file", poolFile(), resourceUuid},
				{"add-pool-to-zone", "--force", "--file", poolFile(), resourceUuid},
				{"delete-pool-from-zone", "--force", resourceUuid, "main"},
			} {
				_, err := runner.run(args...)
				Expect(err).To(MatchError("zone paris is locked. " + explanation))
			}
			Expect(runner.conn.Requests(svczoneconnect.ZoneUpdatePoolOfZoneProcedure)).To(BeEmpty())
			Expect(runner.conn.Requests(svczoneconnect.ZoneAddPoolToZoneProcedure)).To(BeEmpty())
			Expect(runner.conn.Requests(svczoneconnect.ZoneDeletePoolFromZoneProcedure)).To(BeEmpty())
		})

		It("refuses delete-pool-from-zone on a locked pool", func() {
			stubZone(nil, locked)

			_, err := runner.run("delete-pool-from-zone", "--force", resourceUuid, "main")

			Expect(err).To(MatchError("pool main is locked. " + explanation))
		})
	})

	Describe("vips of a locked instance", func() {
		BeforeEach(func() {
			runner.conn.Handle(svcinstanceconnect.InstanceReadInstanceProcedure, reply(&svcinstance.InstanceResponse{
				Instance: &instance.Instance{UUID: resourceUuid, Name: "lb-1", Metadata: locked}}))
			runner.conn.Handle(svcvipconnect.VIPListVIPProcedure,
				reply(&svcvip.ListVIPResponse{Vip: []*vip.VIP{{UUID: "v1", Name: "public"}}}))
			runner.conn.Handle(svcinstanceconnect.InstanceAddVipToInstanceProcedure, reply(&emptypb.Empty{}))
			runner.conn.Handle(svcinstanceconnect.InstanceDeleteVipFromInstanceProcedure, reply(&emptypb.Empty{}))
			runner.conn.Handle(svcvipconnect.VIPRegisterVipProcedure, reply(&svcvip.VIPResponse{}))
		})

		It("refuses add-vip-to-instance, delete-vip-from-instance and register-vip --instance", func() {
			for _, args := range [][]string{
				{"add-vip-to-instance", resourceUuid, "public"},
				{"delete-vip-from-instance", resourceUuid, "public"},
				{"register-vip", "--name", "x", "--instance", resourceUuid},
			} {
				_, err := runner.run(args...)
				Expect(err).To(MatchError("instance lb-1 is locked. " + explanation))
			}
			Expect(runner.conn.Requests(svcinstanceconnect.InstanceAddVipToInstanceProcedure)).To(BeEmpty())
			Expect(runner.conn.Requests(svcinstanceconnect.InstanceDeleteVipFromInstanceProcedure)).To(BeEmpty())
			Expect(runner.conn.Requests(svcvipconnect.VIPRegisterVipProcedure)).To(BeEmpty())
		})
	})
})
