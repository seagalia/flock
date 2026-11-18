package as3_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/seagalia/flock/agent/f5/as3"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	typev1 "github.com/seagalia/flock/sdk/flock/type/v1"
)

func renderBackend(b *backend.Backend) as3.Result {
	return as3.Render(input(newVIP(vipUUID, "10.99.0.1", vsUUID), tcpVS(vsUUID, 443, b.GetUUID()), b))
}

func monitor(res as3.Result) *as3.Monitor {
	m, ok := res.Tenant.Apps[as3.AppName(vipUUID)].Objects[as3.MonitorName(backendUUID)].(*as3.Monitor)
	ExpectWithOffset(1, ok).To(BeTrue(), "monitor not found")
	return m
}

func withHealthCheck(checker any) *backend.Backend {
	b := staticBackend(backendUUID)
	switch c := checker.(type) {
	case *core.HttpHealthCheck:
		b.HealthCheck.HealthChecker = &core.HealthCheck_HttpHealthCheck{HttpHealthCheck: c}
	case *core.TcpHealthCheck:
		b.HealthCheck.HealthChecker = &core.HealthCheck_TcpHealthCheck{TcpHealthCheck: c}
	case *core.UdpHealthCheck:
		b.HealthCheck.HealthChecker = &core.HealthCheck_UdpHealthCheck{UdpHealthCheck: c}
	case *core.GrpcHealthCheck:
		b.HealthCheck.HealthChecker = &core.HealthCheck_GrpcHealthCheck{GrpcHealthCheck: c}
	case *core.PluginHealthCheck:
		b.HealthCheck.HealthChecker = &core.HealthCheck_PluginHealthCheck{PluginHealthCheck: c}
	case *core.NoHealthCheck:
		b.HealthCheck.HealthChecker = &core.HealthCheck_NoHealthCheck{NoHealthCheck: c}
	}
	return b
}

func text(s string) *core.HealthCheckPayload {
	return &core.HealthCheckPayload{Payload: &core.HealthCheckPayload_Text{Text: s}}
}

var _ = Describe("Monitors", func() {
	It("reproduces the flock thresholds with the F5 timeout and time until up", func() {
		b := withHealthCheck(&core.TcpHealthCheck{})
		b.HealthCheck.UnhealthyThreshold = 3
		b.HealthCheck.HealthyThreshold = 2
		b.HealthCheck.AltPort = 9000

		res := renderBackend(b)

		m := monitor(res)
		Expect(m.Interval).To(BeEquivalentTo(10))
		Expect(m.Timeout).To(BeEquivalentTo(32))
		Expect(m.TimeUntilUp).To(BeEquivalentTo(10))
		Expect(m.TargetPort).To(BeEquivalentTo(9000))
		Expect(pool(res, vipUUID, backendUUID).Monitors).To(Equal([]any{as3.UseRef{Use: as3.MonitorName(backendUUID)}}))
	})

	It("rounds durations up to whole seconds, at least one", func() {
		b := withHealthCheck(&core.TcpHealthCheck{})
		b.HealthCheck.Interval = durationpb.New(1500 * time.Millisecond)
		b.HealthCheck.Timeout = durationpb.New(100 * time.Millisecond)
		b.HealthCheck.UnhealthyThreshold = 1

		m := monitor(renderBackend(b))

		Expect(m.Interval).To(BeEquivalentTo(2))
		Expect(m.Timeout).To(BeEquivalentTo(3))
	})

	It("sends an HTTP/1.1 request with the host and expects a 2xx by default", func() {
		m := monitor(renderBackend(withHealthCheck(&core.HttpHealthCheck{Path: "/health", Host: "a.example"})))

		Expect(m.MonitorType).To(Equal("http"))
		Expect(m.Send).To(Equal("GET /health HTTP/1.1\r\nHost: a.example\r\nConnection: close\r\n\r\n"))
		Expect(m.Receive).To(Equal(`^HTTP/1\.[01] (2[0-9][0-9])`))
	})

	It("sends the method, headers and body of the check", func() {
		check := &core.HttpHealthCheck{
			Path: "/h", Host: "a.example", Method: core.RequestMethod_POST, Send: text("ping"),
			RequestHeadersToAdd: []*core.HeaderValueOption{{Header: &core.HeaderValue{Key: "X-Probe", ValueSource: &core.HeaderValue_Value{Value: "1"}}}},
		}

		m := monitor(renderBackend(withHealthCheck(check)))

		Expect(m.Send).To(Equal("POST /h HTTP/1.1\r\nHost: a.example\r\nX-Probe: 1\r\nContent-Length: 4\r\nConnection: close\r\n\r\nping"))
	})

	It("builds the receive pattern from the expected statuses and body", func() {
		check := &core.HttpHealthCheck{
			Path: "/h", Host: "a.example", Receive: text("ok."),
			ExpectedStatuses: &typev1.Int64Range{Start: 200, End: 204},
		}

		Expect(monitor(renderBackend(withHealthCheck(check))).Receive).To(
			Equal(`^HTTP/1\.[01] (200|201|202|203)(.|\r|\n)*ok\.`))
	})

	It("compresses full status decades", func() {
		check := &core.HttpHealthCheck{Path: "/h", Host: "a", ExpectedStatuses: &typev1.Int64Range{Start: 200, End: 312}}

		Expect(monitor(renderBackend(withHealthCheck(check))).Receive).To(
			Equal(`^HTTP/1\.[01] (2[0-9][0-9]|30[0-9]|310|311)`))
	})

	It("sends HTTP/1.0 without Host when the check has no host", func() {
		res := renderBackend(withHealthCheck(&core.HttpHealthCheck{Path: "/health"}))

		Expect(monitor(res).Send).To(Equal("GET /health HTTP/1.0\r\n\r\n"))
		Expect(res.Warnings[backendKey(backendUUID)]).To(ContainElement(
			"health check host is empty, F5 sends HTTP/1.0 without a Host header"))
	})

	It("uses an https monitor over TLS", func() {
		b := withHealthCheck(&core.HttpHealthCheck{Path: "/", Host: "a"})
		b.HealthCheck.EnableTls = true

		Expect(monitor(renderBackend(b)).MonitorType).To(Equal("https"))
	})

	It("falls back to HTTP/1.1 for other codecs", func() {
		res := renderBackend(withHealthCheck(&core.HttpHealthCheck{Path: "/", Host: "a", CodecClientType: typev1.CodecClientType_HTTP2}))

		Expect(res.Warnings[backendKey(backendUUID)]).To(ContainElement(
			"health check codec HTTP2 has no F5 monitor, HTTP/1.1 is used"))
	})

	It("sends and expects the tcp payloads", func() {
		m := monitor(renderBackend(withHealthCheck(&core.TcpHealthCheck{
			Send: text("PING\r\n"), Receive: []*core.HealthCheckPayload{text("+P"), text("ONG")},
		})))

		Expect(m.MonitorType).To(Equal("tcp"))
		Expect(m.Send).To(Equal("PING\r\n"))
		Expect(m.Receive).To(Equal(`^\+PONG`))
	})

	It("keeps a plain tcp monitor for a tcp check over TLS", func() {
		b := withHealthCheck(&core.TcpHealthCheck{})
		b.HealthCheck.EnableTls = true

		res := renderBackend(b)

		Expect(monitor(res).MonitorType).To(Equal("tcp"))
		Expect(res.Warnings[backendKey(backendUUID)]).To(ContainElement(
			"F5 has no tcp monitor over TLS, the tcp check runs without TLS"))
	})

	It("uses an udp monitor", func() {
		m := monitor(renderBackend(withHealthCheck(&core.UdpHealthCheck{Send: text("hello")})))

		Expect(m.MonitorType).To(Equal("udp"))
		Expect(m.Send).To(Equal("hello"))
	})

	DescribeTable("replaces checks F5 can't run with a tcp connect check",
		func(checker any, kind string) {
			res := renderBackend(withHealthCheck(checker))

			Expect(monitor(res).MonitorType).To(Equal("tcp"))
			Expect(monitor(res).Send).To(BeEmpty())
			Expect(res.Warnings[backendKey(backendUUID)]).To(ContainElement(
				kind + " health check has no F5 monitor, a tcp connect check is used"))
		},
		Entry("grpc", &core.GrpcHealthCheck{}, "grpc"),
		Entry("plugin", &core.PluginHealthCheck{Name: "radius"}, "plugin"),
	)

	It("declares no monitor without health check", func() {
		res := renderBackend(withHealthCheck(&core.NoHealthCheck{}))

		Expect(res.Tenant.Apps[as3.AppName(vipUUID)].Objects).ToNot(HaveKey(as3.MonitorName(backendUUID)))
		Expect(pool(res, vipUUID, backendUUID).Monitors).To(BeEmpty())
	})

	It("drops a binary payload", func() {
		binary := &core.HealthCheckPayload{Payload: &core.HealthCheckPayload_Binary{Binary: []byte{0x00, 0x01}}}
		res := renderBackend(withHealthCheck(&core.TcpHealthCheck{Send: binary}))

		Expect(monitor(res).Send).To(BeEmpty())
		Expect(res.Warnings[backendKey(backendUUID)]).To(ContainElement(
			"binary health check payloads can't be sent by an F5 monitor, the payload is left out"))
	})

	It("warns that the unhealthy interval is ignored", func() {
		b := withHealthCheck(&core.TcpHealthCheck{})
		b.HealthCheck.UnhealthyInterval = durationpb.New(time.Second)

		Expect(renderBackend(b).Warnings[backendKey(backendUUID)]).To(ContainElement(
			"health check unhealthy_interval has no F5 equivalent, it is ignored"))
	})
})
