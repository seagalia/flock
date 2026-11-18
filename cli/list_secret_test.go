package cli

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	"github.com/seagalia/flock/sdk/flock/services/backend/v1/svcbackendconnect"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	"github.com/seagalia/flock/sdk/flock/services/cert/v1/svccertconnect"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	"github.com/seagalia/flock/sdk/flock/services/instance/v1/svcinstanceconnect"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	"github.com/seagalia/flock/sdk/flock/services/vip/v1/svcvipconnect"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/flock/services/vs/v1/svcvsconnect"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/flock/services/zone/v1/svczoneconnect"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("secrets", func() {
	var runner *commandRunner

	BeforeEach(func() {
		runner = newCommandRunner()
		runner.conn.Handle(svcvipconnect.VIPListVIPProcedure, firstPage(&svcvip.ListVIPResponse{}))
		runner.conn.Handle(svccertconnect.CertificateListCertificateProcedure, firstPage(&svccert.ListCertificateResponse{}))
		runner.conn.Handle(svczoneconnect.ZoneListZoneProcedure, firstPage(&svczone.ListZoneResponse{}))
		runner.conn.Handle(svcinstanceconnect.InstanceListInstanceProcedure, firstPage(&svcinstance.ListInstanceResponse{}))
		runner.conn.Handle(svcvsconnect.VirtualServerListVSProcedure, firstPage(&svcvs.ListVSResponse{
			Vs: []*vs.VirtualServer{{UUID: "vs-1"}},
		}))
		runner.conn.Handle(svcbackendconnect.BackendListBackendProcedure, firstPage(&svcbackend.ListBackendResponse{
			Backends: []*backend.Backend{{UUID: "b-1"}, {UUID: "b-2"}},
		}))
	})

	It("prints the name, the keys and the used-by count, never a value", func() {
		runner.conn.Handle(svcsecretconnect.SecretListSecretProcedure, firstPage(&svcsecret.ListSecretResponse{
			Secrets: []*secret.Secret{{UUID: resourceUuid, Name: "db", Data: map[string]string{"user": "", "password": "hunter2"}}},
		}))

		out, err := runner.run("secrets")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("db"))
		Expect(out).To(ContainSubstring("password, user"))
		Expect(out).To(MatchRegexp(`password, user\s+\|\s+3`))
		Expect(out).NotTo(ContainSubstring("hunter2"))
	})

	It("counts with the foreign link filter on the secret", func() {
		runner.conn.Handle(svcsecretconnect.SecretListSecretProcedure, firstPage(&svcsecret.ListSecretResponse{
			Secrets: []*secret.Secret{{UUID: resourceUuid, Name: "db"}},
		}))

		_, err := runner.run("secrets")

		Expect(err).NotTo(HaveOccurred())
		Expect(sentFilters(runner, svcvsconnect.VirtualServerListVSProcedure)).To(ConsistOf(
			HaveField("Key", helpers.TypeUrl[*secret.Secret]()),
		))
		Expect(sentFilters(runner, svcvsconnect.VirtualServerListVSProcedure)[0].GetValue()).To(Equal(resourceUuid))
	})

	It("says when there is no secret", func() {
		runner.conn.Handle(svcsecretconnect.SecretListSecretProcedure, firstPage(&svcsecret.ListSecretResponse{}))

		out, err := runner.run("secrets")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("No secrets found."))
	})

	It("prints json without values", func() {
		runner.conn.Handle(svcsecretconnect.SecretListSecretProcedure, firstPage(&svcsecret.ListSecretResponse{
			Secrets: []*secret.Secret{{UUID: resourceUuid, Name: "db", Data: map[string]string{"password": "hunter2"}}},
		}))

		out, err := runner.run("secrets", "--json")

		Expect(err).NotTo(HaveOccurred())
		Expect(out).NotTo(ContainSubstring("hunter2"))
	})
})
