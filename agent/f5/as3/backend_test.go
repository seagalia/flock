package as3_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	f5_backendopts "github.com/seagalia/flock/sdk/flock/extensions/backend/f5_options/v1"
	starling_commopts "github.com/seagalia/flock/sdk/flock/extensions/common/starling_options/v1"
)

var _ = Describe("Backend options", func() {
	It("connects to the endpoints with TLS, verified with the CA of the backend", func() {
		b := staticBackend(backendUUID)
		b.EnableTls = true
		b.Sni = "api.internal"
		b.Ca = "CA-PEM"

		res := renderBackend(b)

		objects := res.Tenant.Apps[as3.AppName(vipUUID)].Objects
		Expect(objects[as3.ClientTLSName(backendUUID)]).To(Equal(&as3.TLSClient{
			Class: "TLS_Client", ServerName: "api.internal", ValidateCertificate: true,
			TrustCA: as3.UseRef{Use: as3.CABundleName(backendUUID)},
		}))
		Expect(objects[as3.CABundleName(backendUUID)]).To(Equal(&as3.CABundle{Class: "CA_Bundle", Bundle: "CA-PEM"}))
		Expect(service(res, vipUUID, vsUUID).ClientTLS).To(Equal(as3.ClientTLSName(backendUUID)))
	})

	It("connects with TLS without verification when the backend has no CA", func() {
		b := staticBackend(backendUUID)
		b.EnableTls = true

		res := renderBackend(b)

		Expect(res.Tenant.Apps[as3.AppName(vipUUID)].Objects[as3.ClientTLSName(backendUUID)]).To(Equal(&as3.TLSClient{Class: "TLS_Client"}))
	})

	It("warns that the connect timeout is ignored", func() {
		b := staticBackend(backendUUID)
		b.ConnectTimeout = durationpb.New(time.Second)

		Expect(renderBackend(b).Warnings[backendKey(backendUUID)]).To(ContainElement(
			"connect_timeout has no F5 pool equivalent, it is ignored"))
	})

	It("uses the monitors of the options instead of the health check", func() {
		b := staticBackend(backendUUID)
		b.TypedOptions = mustAny(&f5_backendopts.BackendOptions{Monitors: []string{"/Common/my_mon"}})

		res := renderBackend(b)

		Expect(pool(res, vipUUID, backendUUID).Monitors).To(Equal([]any{as3.BigipRef{Bigip: "/Common/my_mon"}}))
		Expect(res.Tenant.Apps[as3.AppName(vipUUID)].Objects).ToNot(HaveKey(as3.MonitorName(backendUUID)))
	})

	It("uses the lb mode of the options without warning", func() {
		b := staticBackend(backendUUID)
		b.LbPolicy = backend.Backend_MAGLEV
		b.TypedOptions = mustAny(&f5_backendopts.BackendOptions{LbMode: "observed-member"})

		res := renderBackend(b)

		Expect(pool(res, vipUUID, backendUUID).LoadBalancingMode).To(Equal("observed-member"))
		Expect(service(res, vipUUID, vsUUID).PersistenceMethods).To(BeEmpty())
		Expect(res.Warnings[backendKey(backendUUID)]).To(BeEmpty())
	})

	It("sets the slow ramp time in seconds", func() {
		b := staticBackend(backendUUID)
		b.TypedOptions = mustAny(&f5_backendopts.BackendOptions{SlowRampTime: durationpb.New(1500 * time.Millisecond)})

		Expect(pool(renderBackend(b), vipUUID, backendUUID).SlowRampTime).To(BeEquivalentTo(2))
	})

	It("ignores a starling extension without a warning", func() {
		b := staticBackend(backendUUID)
		b.HttpProtocolOptions = mustAny(&starling_commopts.HttpProtocolOptions{})

		Expect(renderBackend(b).Warnings[backendKey(backendUUID)]).To(BeEmpty())
	})
})
