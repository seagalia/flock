package bigip_test

import (
	"context"
	"errors"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/seagalia/flock/agent/f5/bigip"
	"github.com/seagalia/flock/agent/f5/bigip/bigipmock"
)

const tenantBody = `{"class":"AS3","action":"deploy","persist":true,"declaration":{"class":"ADC","schemaVersion":"3.50.0","id":"flock-inst-1","flock":{"class":"Tenant","remark":"flock instance inst-1"}}}`

// newClient talks to the mocks in this order. httptest serves the same certificate on every server, so the TLS
// config of the first one trusts them all. The failover cache is off so a failover shows on the next call.
func newClient(mocks ...*bigipmock.Mock) *bigip.Client {
	urls := make([]string, 0, len(mocks))
	for _, m := range mocks {
		urls = append(urls, m.URL())
	}
	return bigip.New(bigip.Options{
		URLs: urls, Username: bigipmock.Username, Password: bigipmock.Password, TLSConfig: mocks[0].TLSConfig(),
		RequestTimeout: 5 * time.Second, PollInterval: 10 * time.Millisecond, FailoverCacheTTL: -1,
	})
}

var _ = Describe("Client", func() {
	var (
		ctx    context.Context
		device *bigipmock.Mock
		client *bigip.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		device = bigipmock.New()
		DeferCleanup(device.Close)
		client = newClient(device)
	})

	It("logs in once and reuses the token", func() {
		_, err := client.ListPartitions(ctx)
		Expect(err).ToNot(HaveOccurred())
		_, err = client.ListTenants(ctx)
		Expect(err).ToNot(HaveOccurred())

		Expect(device.Logins()).To(Equal(1))
	})

	It("logs in again when the token expired", func() {
		_, err := client.ListPartitions(ctx)
		Expect(err).ToNot(HaveOccurred())
		device.ExpireToken()

		partitions, err := client.ListPartitions(ctx)

		Expect(err).ToNot(HaveOccurred())
		Expect(partitions).To(ContainElement("Common"))
		Expect(device.Logins()).To(Equal(2))
	})

	It("never puts the password in an error", func() {
		wrong := bigip.New(bigip.Options{URLs: []string{device.URL()}, Username: "admin", Password: "s3cr3t-pass",
			TLSConfig: device.TLSConfig(), RequestTimeout: 5 * time.Second})

		_, err := wrong.ListPartitions(ctx)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).ToNot(ContainSubstring("s3cr3t-pass"))
	})

	Describe("Declare", func() {
		It("declares a tenant and tells when nothing changed", func() {
			res, err := client.Declare(ctx, "flock", []byte(tenantBody))
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Changed).To(BeTrue())
			Expect(device.Tenants()).To(HaveKey("flock"))

			res, err = client.Declare(ctx, "flock", []byte(tenantBody))

			Expect(err).ToNot(HaveOccurred())
			Expect(res.Changed).To(BeFalse())
			Expect(res.Message).To(Equal("no change"))
		})

		It("reports a refusal of an unlicensed device", func() {
			device.FailNextTask(bigipmock.NotLicensedResult)

			_, err := client.Declare(ctx, "flock", []byte(tenantBody))

			var bigipErr *bigip.Error
			Expect(errors.As(err, &bigipErr)).To(BeTrue())
			Expect(bigipErr.Status).To(Equal(422))
			Expect(bigipErr.Code).To(Equal("01070356"))
			Expect(bigip.IsNotLicensed(err)).To(BeTrue())
			Expect(bigipErr.Retryable()).To(BeFalse())
		})

		It("reports the errors of an invalid declaration", func() {
			device.FailNext(http.StatusUnprocessableEntity, bigipmock.InvalidDeclaration)

			_, err := client.Declare(ctx, "flock", []byte(tenantBody))

			Expect(err).To(MatchError(ContainSubstring("declaration is invalid: /probe_tenant/probe_app/web_pool/members/0/servicePort: should be integer")))
			var bigipErr *bigip.Error
			Expect(errors.As(err, &bigipErr)).To(BeTrue())
			Expect(bigipErr.Retryable()).To(BeFalse())
		})

		It("tells a busy device can be retried", func() {
			device.SetBusy(1)

			_, err := client.Declare(ctx, "flock", []byte(tenantBody))

			var bigipErr *bigip.Error
			Expect(errors.As(err, &bigipErr)).To(BeTrue())
			Expect(bigipErr.Status).To(Equal(503))
			Expect(bigipErr.Retryable()).To(BeTrue())
		})
	})

	It("lists the AS3 tenants with their remark", func() {
		tenants, err := client.ListTenants(ctx)
		Expect(err).ToNot(HaveOccurred())
		Expect(tenants).To(BeEmpty())

		device.AddTenant("team1", "someone else")

		Expect(client.ListTenants(ctx)).To(Equal(map[string]string{"team1": "someone else"}))
	})

	It("deletes a tenant, a missing one included", func() {
		_, err := client.Declare(ctx, "flock", []byte(tenantBody))
		Expect(err).ToNot(HaveOccurred())

		Expect(client.DeleteTenant(ctx, "flock")).To(Succeed())
		Expect(device.Tenants()).To(BeEmpty())
		Expect(client.DeleteTenant(ctx, "flock")).To(Succeed())
	})

	It("lists the partitions", func() {
		device.AddPartition("team1")

		Expect(client.ListPartitions(ctx)).To(ConsistOf("Common", "team1"))
	})

	It("reads the states of the members of a pool", func() {
		device.SetMemberStates("flock", "vip_a", "pool_b", map[string]bool{"10.0.0.10:8080": true, "[2001:db8::1]:8080": false})

		Expect(client.PoolMemberStates(ctx, "flock", "vip_a", "pool_b")).To(Equal(map[string]bool{
			"10.0.0.10:8080": true, "[2001:db8::1]:8080": false,
		}))
	})

	Describe("HA pair", func() {
		var standby, active *bigipmock.Mock

		BeforeEach(func() {
			standby, active = bigipmock.New(), bigipmock.New()
			DeferCleanup(standby.Close)
			DeferCleanup(active.Close)
			standby.SetActive(false)
			client = newClient(standby, active)
		})

		It("sends to the active device and follows a failover", func() {
			_, err := client.Declare(ctx, "flock", []byte(tenantBody))
			Expect(err).ToNot(HaveOccurred())
			Expect(active.Declares()).To(HaveLen(1))
			Expect(standby.Declares()).To(BeEmpty())

			standby.SetActive(true)
			active.SetActive(false)
			_, err = client.Declare(ctx, "flock", []byte(tenantBody))

			Expect(err).ToNot(HaveOccurred())
			Expect(standby.Declares()).To(HaveLen(1))
		})

		It("uses the first reachable device when none is active", func() {
			active.SetActive(false)

			_, err := client.Declare(ctx, "flock", []byte(tenantBody))

			Expect(err).ToNot(HaveOccurred())
			Expect(standby.Declares()).To(HaveLen(1))
		})

		It("skips an unreachable device", func() {
			standby.Close()

			_, err := client.Declare(ctx, "flock", []byte(tenantBody))

			Expect(err).ToNot(HaveOccurred())
			Expect(active.Declares()).To(HaveLen(1))
		})

		It("reports the health of each device", func() {
			active.SetAS3Version("3.45.0")

			devices := client.Devices(ctx)

			Expect(devices).To(HaveLen(2))
			Expect(devices[0]).To(Equal(bigip.DeviceHealth{URL: standby.URL(), Reachable: true, FailoverStatus: "STANDBY", AS3Version: "3.56.0"}))
			Expect(devices[1]).To(Equal(bigip.DeviceHealth{URL: active.URL(), Reachable: true, Active: true, FailoverStatus: "ACTIVE", AS3Version: "3.45.0"}))
		})
	})
})
