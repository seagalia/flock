package vs_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/limit/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	. "github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/limit/starling_limit/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

func packed(message proto.Message) *anypb.Any {
	result, err := anypb.New(message)
	Expect(err).ToNot(HaveOccurred())
	return result
}

var (
	httpVs = &VirtualServer{RouteConfig: &VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &HttpRoutesConfig{
		VirtualHosts: []*route.VirtualHost{
			{
				Routes:     []*route.Route{{Action: &route.Route_BackendUuid{BackendUuid: "backend-1"}}},
				TlsOptions: []*core.TLS{{CertificatesUuid: []string{"cert-http"}}},
			},
			{Routes: []*route.Route{{Action: &route.Route_DirectResponse{DirectResponse: &route.DirectResponseAction{Status: 404}}}}},
		},
	}}}
	tlsTcpVs = &VirtualServer{RouteConfig: &VirtualServer_TlsTcpRoutesConfig{TlsTcpRoutesConfig: &TlsTcpRoutesConfig{
		Routes: []*TlsTcpRouteConfig{
			{BackendUuid: "backend-1", Mode: &TlsTcpRouteConfig_Tls{Tls: &core.TLS{CertificatesUuid: []string{"cert-1", "cert-2"}}}},
			{BackendUuid: "backend-2"},
		},
	}}}
	tcpVs = &VirtualServer{RouteConfig: &VirtualServer_TcpRouteConfig{TcpRouteConfig: &TcpRouteConfig{BackendUuid: "backend-tcp"}}}
	udpVs = &VirtualServer{RouteConfig: &VirtualServer_UdpRouteConfig{UdpRouteConfig: &UdpRouteConfig{BackendUuid: "backend-udp"}}}
)

var _ = Describe("VirtualServer", func() {
	It("should be identified by its uuid", func() {
		aVs := &VirtualServer{UUID: "vs-1"}
		Expect(aVs.GetIdentifier()).To(Equal("vs-1"))
		aVs.SetIdentifier("vs-2")
		Expect(aVs.GetUUID()).To(Equal("vs-2"))
		expectedSig, err := helpers.MessageSignature(aVs)
		Expect(err).ToNot(HaveOccurred())
		Expect(aVs.Signature()).To(Equal(expectedSig))
	})

	DescribeTable("should give backends used by its routes",
		func(aVs *VirtualServer, expected []string) {
			Expect(aVs.GetBackendUuids()).To(Equal(expected))
		},
		Entry("http routes with a backend", httpVs, []string{"backend-1"}),
		Entry("tls tcp routes", tlsTcpVs, []string{"backend-1", "backend-2"}),
		Entry("tcp route", tcpVs, []string{"backend-tcp"}),
		Entry("udp route", udpVs, []string{"backend-udp"}),
		Entry("no route", &VirtualServer{}, []string{}),
	)

	It("should give certificates of tls tcp routes and of virtual hosts", func() {
		Expect(tlsTcpVs.GetCertificatesUuids()).To(Equal([]string{"cert-1", "cert-2"}))
		Expect(httpVs.GetCertificatesUuids()).To(Equal([]string{"cert-http"}))
		Expect(tcpVs.GetCertificatesUuids()).To(BeEmpty())
	})

	It("should give its route config", func() {
		Expect(httpVs.RetrieveRouteConfig()).To(BeIdenticalTo(httpVs.GetHttpRoutesConfig()))
		Expect(tlsTcpVs.RetrieveRouteConfig()).To(BeIdenticalTo(tlsTcpVs.GetTlsTcpRoutesConfig()))
		Expect(tcpVs.RetrieveRouteConfig()).To(BeIdenticalTo(tcpVs.GetTcpRouteConfig()))
		Expect(udpVs.RetrieveRouteConfig()).To(BeIdenticalTo(udpVs.GetUdpRouteConfig()))
		Expect((&VirtualServer{}).RetrieveRouteConfig()).To(BeNil())
	})

	It("should not panic on a nil virtual server", func() {
		var nilVs *VirtualServer
		Expect(nilVs.GetBackendUuids()).To(BeEmpty())
		Expect(nilVs.GetCertificatesUuids()).To(BeEmpty())
		Expect(nilVs.RetrieveRouteConfig()).To(BeNil())
	})

	It("should reference certificates, backends and virtual hosts as foreign resources", func() {
		Expect(httpVs.ForeignResources()).To(Equal(map[string][]string{
			helpers.TypeUrl[*cert.Certificate](): {"cert-http"},
			helpers.TypeUrl[*backend.Backend]():  {"backend-1"},
		}))
	})

	It("should manage metadata", func() {
		aVs := &VirtualServer{}
		Expect(aVs.DeleteMetadata("key")).To(BeNil())
		aVs.AddMetadata("key", "value")
		Expect(aVs.GetMetadata()).To(HaveKeyWithValue("key", "value"))
		Expect(aVs.DeleteMetadata("key")).To(BeEmpty())
		aVs.SetMetadata(map[string]string{"other": "value"})
		Expect(aVs.GetMetadata()).To(HaveKey("other"))
	})

	Context("limits", func() {
		var aVs *VirtualServer

		BeforeEach(func() {
			aVs = &VirtualServer{LimitConfig: []*limit.Config{
				{Action: &limit.Config_TypedLimit{TypedLimit: packed(wrapperspb.String("not a limit"))}},
				{Action: &limit.Config_ConnectionRateLimit{ConnectionRateLimit: &limit.ConnectionRateLimit{Rate: 10}}},
				{Action: &limit.Config_ConnectionLimit{ConnectionLimit: &limit.ConnectionLimit{MaxConnections: 100}}},
				{Action: &limit.Config_TypedLimit{TypedLimit: packed(&starling_limit.StarlingRateLimit{})}},
			}}
		})

		It("should give the first limit of each kind", func() {
			Expect(aVs.GetFirstConnLimit().GetMaxConnections()).To(BeEquivalentTo(100))
			Expect(aVs.GetFirstConnRateLimit().GetRate()).To(BeEquivalentTo(10))
			Expect(aVs.GetStarlingRateLimit()).ToNot(BeNil())
		})

		It("should give nothing without limits", func() {
			noLimit := &VirtualServer{}
			Expect(noLimit.GetFirstConnLimit()).To(BeNil())
			Expect(noLimit.GetFirstConnRateLimit()).To(BeNil())
			Expect(noLimit.GetStarlingRateLimit()).To(BeNil())
		})
	})
})

var _ = Describe("Route configs options", func() {
	wrongType := func() *anypb.Any { return packed(wrapperspb.String("other")) }

	It("should give empty options when not set", func() {
		Expect((&TcpRouteConfig{}).GetStarlingOptions()).ToNot(BeNil())
		Expect((&UdpRouteConfig{}).GetStarlingOptions()).ToNot(BeNil())
		Expect((&TlsTcpRoutesConfig{}).GetStarlingOptions()).ToNot(BeNil())
		Expect((&HttpRoutesConfig{}).GetStarlingOptions()).ToNot(BeNil())
	})

	It("should unpack options of the right type", func() {
		_, err := (&TcpRouteConfig{TypedOptions: packed(&starling_vsopts.TcpUdpOptions{})}).GetStarlingOptions()
		Expect(err).ToNot(HaveOccurred())
		_, err = (&UdpRouteConfig{TypedOptions: packed(&starling_vsopts.TcpUdpOptions{})}).GetStarlingOptions()
		Expect(err).ToNot(HaveOccurred())
		_, err = (&TlsTcpRoutesConfig{TypedOptions: packed(&starling_vsopts.TlsTcpOptions{})}).GetStarlingOptions()
		Expect(err).ToNot(HaveOccurred())
		_, err = (&HttpRoutesConfig{TypedOptions: packed(&starling_vsopts.HttpOptions{})}).GetStarlingOptions()
		Expect(err).ToNot(HaveOccurred())
	})

	It("should fail on options of another type", func() {
		_, err := (&TcpRouteConfig{TypedOptions: wrongType()}).GetStarlingOptions()
		Expect(err).To(HaveOccurred())
		_, err = (&UdpRouteConfig{TypedOptions: wrongType()}).GetStarlingOptions()
		Expect(err).To(HaveOccurred())
		_, err = (&TlsTcpRoutesConfig{TypedOptions: wrongType()}).GetStarlingOptions()
		Expect(err).To(HaveOccurred())
		_, err = (&HttpRoutesConfig{TypedOptions: wrongType()}).GetStarlingOptions()
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("External auth of an http virtual server", func() {
	var (
		cfg        *HttpRoutesConfig
		vh         *route.VirtualHost
		inherits   *route.Route
		own        *route.Route
		disabled   *route.Route
		unknown    *route.Route
		httpAuthVs *VirtualServer
	)

	BeforeEach(func() {
		inherits = &route.Route{Action: &route.Route_BackendUuid{BackendUuid: "b-app"}}
		own = &route.Route{
			Action:       &route.Route_BackendUuid{BackendUuid: "b-app"},
			ExternalAuth: &route.Route_ExternalAuthName{ExternalAuthName: "other"},
		}
		disabled = &route.Route{
			Action:       &route.Route_BackendUuid{BackendUuid: "b-app"},
			ExternalAuth: &route.Route_ExternalAuthDisabled{ExternalAuthDisabled: true},
		}
		unknown = &route.Route{
			Action:       &route.Route_BackendUuid{BackendUuid: "b-app"},
			ExternalAuth: &route.Route_ExternalAuthName{ExternalAuthName: "missing"},
		}
		vh = &route.VirtualHost{
			ExternalAuth: "sso",
			Routes:       []*route.Route{inherits, own, disabled, unknown},
		}
		cfg = &HttpRoutesConfig{
			VirtualHosts: []*route.VirtualHost{vh},
			ExternalAuths: []*route.ExternalAuth{
				{
					Name:            "sso",
					BackendUuid:     "b-auth",
					UpstreamHeaders: []string{"x-auth-request-user", "x-auth-request-email"},
				},
				{Name: "other", BackendUuid: "b-app", UpstreamHeaders: []string{"x-auth-request-user", "x-user"}},
			},
		}
		httpAuthVs = &VirtualServer{RouteConfig: &VirtualServer_HttpRoutesConfig{HttpRoutesConfig: cfg}}
	})

	It("lists the policy backends after the route backends, once", func() {
		vhost := &route.VirtualHost{Routes: []*route.Route{inherits}}
		withPolicies := &VirtualServer{RouteConfig: &VirtualServer_HttpRoutesConfig{HttpRoutesConfig: &HttpRoutesConfig{
			VirtualHosts:  []*route.VirtualHost{vhost},
			ExternalAuths: cfg.ExternalAuths,
		}}}
		Expect(withPolicies.GetBackendUuids()).To(Equal([]string{"b-app", "b-auth"}))
	})

	It("resolves the effective policy of a route", func() {
		policies := route.ExternalAuths(cfg.GetExternalAuths())
		Expect(policies.Of(vh, inherits).GetName()).To(Equal("sso"))
		Expect(policies.Of(vh, own).GetName()).To(Equal("other"))
		Expect(policies.Of(vh, disabled)).To(BeNil())
		Expect(policies.Of(vh, unknown)).To(BeNil())
		Expect(policies.Of(&route.VirtualHost{}, inherits)).To(BeNil())
	})

	It("gives the policies checking a route of the rendered virtual hosts, in their order", func() {
		policies := route.ExternalAuths(cfg.GetExternalAuths())
		Expect(policies.Used(cfg.GetVirtualHosts())).To(HaveExactElements(cfg.ExternalAuths[0], cfg.ExternalAuths[1]))
		onlyOwn := &route.VirtualHost{Routes: []*route.Route{own, disabled}}
		Expect(policies.Used([]*route.VirtualHost{onlyOwn})).To(HaveExactElements(cfg.ExternalAuths[1]))
		Expect(policies.Used(nil)).To(BeEmpty())
	})

	It("strips the union of the upstream headers", func() {
		Expect(route.ExternalAuths(cfg.GetExternalAuths()).StrippedHeaders()).To(Equal([]string{
			"x-auth-request-email", "x-auth-request-user", "x-user",
		}))
		Expect(route.ExternalAuths(nil).StrippedHeaders()).To(BeEmpty())
	})

	It("defaults and lowercases the policies of a virtual server", func() {
		policy := &route.ExternalAuth{
			Name:            "p",
			RequestHeaders:  []string{"Cookie"},
			UpstreamHeaders: []string{"X-Auth-Request-User"},
			ClientHeaders:   []string{"Set-Cookie"},
		}
		cfg.ExternalAuths = []*route.ExternalAuth{policy}
		httpAuthVs.ProvDefault()
		Expect(policy.GetTimeout().AsDuration()).To(Equal(time.Second))
		Expect(policy.GetStatusOnError()).To(Equal(uint32(503)))
		Expect(policy.GetRequestHeaders()).To(Equal([]string{"cookie"}))
		Expect(policy.GetUpstreamHeaders()).To(Equal([]string{"x-auth-request-user"}))
		Expect(policy.GetClientHeaders()).To(Equal([]string{"set-cookie"}))
		var nilVs *VirtualServer
		Expect(nilVs.ProvDefault).ToNot(Panic())
	})
})
