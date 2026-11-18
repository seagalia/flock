package envoyctl

import (
	"time"

	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	ext_procv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	starling_route "github.com/seagalia/flock/sdk/flock/extensions/route/starling_route/v1"
	starling_vsopts "github.com/seagalia/flock/sdk/flock/extensions/vs/starling_options/v1"
	"google.golang.org/protobuf/types/known/anypb"
)

var _ = Describe("WAF conversion", func() {
	routeWithWaf := func(name string, waf *starling_route.Waf) *route.Route {
		rte := &route.Route{
			Name:   name,
			Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/" + name}},
			Action: &route.Route_BackendUuid{BackendUuid: "backend-1"},
		}
		if waf == nil {
			return rte
		}
		routeOpts, err := anypb.New(&starling_route.VirtualHostRouteOptions{Waf: waf})
		Expect(err).ToNot(HaveOccurred())
		rte.TypedOptions = routeOpts
		return rte
	}
	httpRoutes := func(routes ...*route.Route) []*HttpRoute {
		return []*HttpRoute{{
			Name:        "vs-0",
			VirtualHost: &route.VirtualHost{Domains: []string{"a.local"}, Routes: routes},
			Backends:    []*backend.Backend{{UUID: "backend-1"}},
		}}
	}
	findRoute := func(envoyRoutes []*routev3.Route, name string) *routev3.Route {
		GinkgoHelper()
		for _, envoyRoute := range envoyRoutes {
			if envoyRoute.GetName() == name {
				return envoyRoute
			}
		}
		Fail("no route " + name)
		return nil
	}
	routePerFilter := func(envoyRoutes []*routev3.Route, name string) *ext_procv3.ExtProcPerRoute {
		GinkgoHelper()
		envoyRoute := findRoute(envoyRoutes, name)
		return unmarshalAny(envoyRoute.GetTypedPerFilterConfig()[wafFilterName], &ext_procv3.ExtProcPerRoute{})
	}

	It("should add no filter when no route has a WAF", func() {
		httpMan := virtualHostToHttpManager("vs-uuid", nil, httpRoutes(routeWithWaf("open", nil)), &starling_vsopts.HttpOptions{}, nil)
		expectValidEnvoyConfig(httpMan)

		Expect(httpMan.GetHttpFilters()).To(HaveLen(1))
		Expect(httpMan.GetHttpFilters()[0].GetName()).To(Equal("envoy.filters.http.router"))
		envoyRoutes := httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()
		Expect(envoyRoutes[0].GetTypedPerFilterConfig()).To(BeEmpty())
	})

	It("should enable the WAF only on the routes with one", func() {
		routes := httpRoutes(
			routeWithWaf("open", nil),
			routeWithWaf("default", &starling_route.Waf{}),
			routeWithWaf("detect", &starling_route.Waf{Config: &starling_route.Waf_DetectOnly{DetectOnly: true}}),
			routeWithWaf("custom", &starling_route.Waf{Config: &starling_route.Waf_CustomRules{CustomRules: "SecRuleEngine On"}}),
		)
		httpMan := virtualHostToHttpManager("vs-uuid", nil, routes, &starling_vsopts.HttpOptions{}, nil)
		expectValidEnvoyConfig(httpMan)

		filters := httpMan.GetHttpFilters()
		Expect(filters).To(HaveLen(2))
		Expect(filters[0].GetName()).To(Equal(wafFilterName))
		Expect(filters[0].GetDisabled()).To(BeTrue())
		extProc := unmarshalAny(filters[0].GetTypedConfig(), &ext_procv3.ExternalProcessor{})
		Expect(extProc.GetGrpcService().GetEnvoyGrpc().GetClusterName()).To(Equal(wafClusterName))
		Expect(extProc.GetFailureModeAllow()).To(BeFalse())
		Expect(extProc.GetMessageTimeout().AsDuration()).To(Equal(200 * time.Millisecond))
		Expect(extProc.GetMaxMessageTimeout().AsDuration()).To(Equal(10 * time.Second))
		Expect(extProc.GetProcessingMode().GetRequestBodyMode()).To(Equal(ext_procv3.ProcessingMode_BUFFERED))
		Expect(extProc.GetProcessingMode().GetResponseBodyMode()).To(Equal(ext_procv3.ProcessingMode_BUFFERED_PARTIAL))
		Expect(extProc.GetRequestAttributes()).To(ContainElements("source.address", "request.path"))

		envoyRoutes := httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()
		Expect(envoyRoutes[0].GetTypedPerFilterConfig()).To(BeEmpty())
		Expect(routePerFilter(envoyRoutes, "default").GetOverrides().GetGrpcInitialMetadata()).To(BeEmpty())
		for _, name := range []string{"detect", "custom"} {
			metadata := routePerFilter(envoyRoutes, name).GetOverrides().GetGrpcInitialMetadata()
			Expect(metadata).To(HaveLen(1))
			Expect(metadata[0].GetKey()).To(Equal("x-waf-uuid"))
			Expect(metadata[0].GetValue()).To(Equal(WafIdentifier("vs-uuid", 0, name)))
		}
	})

	It("should buffer 64 KiB of request body and inspect bodies whole by default", func() {
		httpMan := virtualHostToHttpManager("vs-uuid", nil, httpRoutes(routeWithWaf("default", &starling_route.Waf{})), &starling_vsopts.HttpOptions{}, nil)
		expectValidEnvoyConfig(httpMan)

		envoyRoutes := httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()
		Expect(findRoute(envoyRoutes, "default").GetRequestBodyBufferLimit().GetValue()).To(BeEquivalentTo(64 << 10))
		overrides := routePerFilter(envoyRoutes, "default").GetOverrides()
		Expect(overrides.GetProcessingMode().GetRequestHeaderMode()).To(Equal(ext_procv3.ProcessingMode_SEND))
		Expect(overrides.GetProcessingMode().GetResponseHeaderMode()).To(Equal(ext_procv3.ProcessingMode_SEND))
		Expect(overrides.GetProcessingMode().GetRequestBodyMode()).To(Equal(ext_procv3.ProcessingMode_BUFFERED))
		Expect(overrides.GetProcessingMode().GetResponseBodyMode()).To(Equal(ext_procv3.ProcessingMode_BUFFERED_PARTIAL))
		Expect(overrides.GetFailureModeAllow().GetValue()).To(BeFalse())
	})

	It("should apply the body options of a route", func() {
		waf := &starling_route.Waf{
			RequestBodyLimit:   4 << 20,
			PartialRequestBody: true,
			SkipResponseBody:   true,
			FailOpen:           true,
		}
		httpMan := virtualHostToHttpManager("vs-uuid", nil, httpRoutes(routeWithWaf("upload", waf)), &starling_vsopts.HttpOptions{}, nil)
		expectValidEnvoyConfig(httpMan)

		envoyRoutes := httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()
		Expect(findRoute(envoyRoutes, "upload").GetRequestBodyBufferLimit().GetValue()).To(BeEquivalentTo(4 << 20))
		overrides := routePerFilter(envoyRoutes, "upload").GetOverrides()
		Expect(overrides.GetProcessingMode().GetRequestBodyMode()).To(Equal(ext_procv3.ProcessingMode_BUFFERED_PARTIAL))
		Expect(overrides.GetProcessingMode().GetResponseBodyMode()).To(Equal(ext_procv3.ProcessingMode_NONE))
		Expect(overrides.GetFailureModeAllow().GetValue()).To(BeTrue())
	})

	It("should leave the buffer limit of a route without WAF", func() {
		httpMan := virtualHostToHttpManager("vs-uuid", nil, httpRoutes(routeWithWaf("open", nil)), &starling_vsopts.HttpOptions{}, nil)

		envoyRoutes := httpMan.GetRouteConfig().GetVirtualHosts()[0].GetRoutes()
		Expect(findRoute(envoyRoutes, "open").GetRequestBodyBufferLimit()).To(BeNil())
	})
})
