package route_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	. "github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

var _ = Describe("VirtualHost", func() {
	It("should give the backends its routes go to", func() {
		vh := &VirtualHost{Routes: []*Route{
			{Action: &Route_BackendUuid{BackendUuid: "backend-1"}},
			{Action: &Route_DirectResponse{DirectResponse: &DirectResponseAction{Status: 404}}},
			{Action: &Route_BackendUuid{BackendUuid: "backend-2"}},
		}}
		Expect(vh.GetBackendUuids()).To(Equal([]string{"backend-1", "backend-2"}))
		var nilVh *VirtualHost
		Expect(nilVh.GetBackendUuids()).To(BeEmpty())
	})

	It("should give certificates of every tls options as foreign resources", func() {
		vh := &VirtualHost{TlsOptions: []*core.TLS{
			{CertificatesUuid: []string{"cert-1"}},
			{},
			{CertificatesUuid: []string{"cert-2", "cert-3"}},
		}}
		Expect(vh.GetCertificatesUuids()).To(Equal([]string{"cert-1", "cert-2", "cert-3"}))
		Expect(vh.ForeignResources()).To(Equal(map[string][]string{
			helpers.TypeUrl[*cert.Certificate](): {"cert-1", "cert-2", "cert-3"},
		}))
		var nilVh *VirtualHost
		Expect(nilVh.GetCertificatesUuids()).To(BeEmpty())
	})

	It("should match every domains by default", func() {
		vh := &VirtualHost{}
		vh.ProvDefault()
		Expect(vh.GetDomains()).To(Equal([]string{"*"}))

		withDomain := &VirtualHost{Domains: []string{"example.com"}}
		withDomain.ProvDefault()
		Expect(withDomain.GetDomains()).To(Equal([]string{"example.com"}))

		var nilVh *VirtualHost
		Expect(nilVh.ProvDefault).ToNot(Panic())
	})

	It("should manage metadata", func() {
		vh := &VirtualHost{}
		Expect(vh.DeleteMetadata("key")).To(BeNil())
		vh.AddMetadata("key", "value")
		Expect(vh.GetMetadata()).To(HaveKeyWithValue("key", "value"))
		Expect(vh.DeleteMetadata("key")).To(BeEmpty())
		vh.SetMetadata(map[string]string{"other": "value"})
		Expect(vh.GetMetadata()).To(HaveKey("other"))
	})
})

var _ = Describe("Route", func() {
	It("should accept a redirect of the path only", func() {
		rte := &Route{
			Name:   "old",
			Match:  &RouteMatch{PathSpecifier: &RouteMatch_Prefix{Prefix: "/old"}},
			Action: &Route_Redirect{Redirect: &RedirectAction{PathRedirect: "/new"}},
		}
		Expect(helpers.Validate(rte)).To(Succeed())

		rte.GetRedirect().HostRedirect = "bad\nhost"
		Expect(helpers.Validate(rte)).ToNot(Succeed())
	})

	It("should give its starling agent options", func() {
		options, err := (&Route{}).GetStarlingOptions()
		Expect(err).ToNot(HaveOccurred())
		Expect(options).ToNot(BeNil())

		packed, err := anypb.New(&starling_route.VirtualHostRouteOptions{})
		Expect(err).ToNot(HaveOccurred())
		_, err = (&Route{TypedOptions: packed}).GetStarlingOptions()
		Expect(err).ToNot(HaveOccurred())

		wrong, err := anypb.New(wrapperspb.String("other"))
		Expect(err).ToNot(HaveOccurred())
		_, err = (&Route{TypedOptions: wrong}).GetStarlingOptions()
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ExternalAuth", func() {
	validPolicy := func() *ExternalAuth {
		p := &ExternalAuth{Name: "sso", BackendUuid: "b-auth", Path: "/"}
		p.ProvDefault()
		return p
	}

	It("should accept a valid policy", func() {
		Expect(helpers.Validate(validPolicy())).To(Succeed())
	})

	It("should accept status 429 on error", func() {
		p := validPolicy()
		p.StatusOnError = 429
		Expect(helpers.Validate(p)).To(Succeed())
	})

	It("should default the timeout and the error status of an empty policy", func() {
		p := &ExternalAuth{}
		p.ProvDefault()
		Expect(p.GetTimeout().AsDuration()).To(Equal(time.Second))
		Expect(p.GetStatusOnError()).To(Equal(uint32(503)))
		var nilPolicy *ExternalAuth
		Expect(nilPolicy.ProvDefault).ToNot(Panic())
	})

	DescribeTable("should refuse an invalid policy", func(mutate func(*ExternalAuth)) {
		p := validPolicy()
		mutate(p)
		Expect(helpers.Validate(p)).To(HaveOccurred())
	},
		Entry("upper case name", func(p *ExternalAuth) { p.Name = "SSO" }),
		Entry("path without slash", func(p *ExternalAuth) { p.Path = "check" }),
		Entry("timeout over 30s", func(p *ExternalAuth) { p.Timeout = durationpb.New(31 * time.Second) }),
		Entry("status 418 on error", func(p *ExternalAuth) { p.StatusOnError = 418 }),
		Entry("status 599 on error", func(p *ExternalAuth) { p.StatusOnError = 599 }),
		Entry("status 302 on error", func(p *ExternalAuth) { p.StatusOnError = 302 }),
		Entry("host as upstream header", func(p *ExternalAuth) { p.UpstreamHeaders = []string{"host"} }),
		Entry("pseudo header", func(p *ExternalAuth) { p.UpstreamHeaders = []string{":path"} }),
		Entry("hop by hop header", func(p *ExternalAuth) { p.UpstreamHeaders = []string{"transfer-encoding"} }),
		Entry("space in a header name", func(p *ExternalAuth) { p.RequestHeaders = []string{"x user"} }),
	)
})
