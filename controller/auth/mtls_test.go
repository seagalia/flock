package auth_test

import (
	"context"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/sdk/contexes"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
)

var _ = Describe("IsMTlsConnection", func() {
	// agentCall gives the server context of a real call from a client with a certificate named commonName
	agentCall := func(commonName string, header map[string]string) context.Context {
		serverTLS, clientTLS := rpctest.NewTLS(commonName)
		ctx, err := rpctest.ServerContextOverHTTP(serverTLS, clientTLS, header)
		Expect(err).ToNot(HaveOccurred())
		return ctx
	}

	It("should identify agent from its client certificate", func() {
		newCtx, ok := IsMTlsConnection(agentCall("agent1", nil))

		Expect(ok).To(BeTrue())
		username, groups, ok := contexes.RetrieveUserInfo(newCtx)
		Expect(ok).To(BeTrue())
		Expect(username).To(MatchRegexp(`^127\.0\.0\.1:\d+-agent1$`))
		Expect(groups).To(Equal([]string{"agent-agent1"}))
	})
	It("should use real ip from headers in username", func() {
		newCtx, ok := IsMTlsConnection(agentCall("agent1", map[string]string{"X-Real-IP": "192.168.1.1"}))

		Expect(ok).To(BeTrue())
		username, _, ok := contexes.RetrieveUserInfo(newCtx)
		Expect(ok).To(BeTrue())
		Expect(username).To(Equal("192.168.1.1-agent1"))
	})
	It("should use first forwarded ip in username", func() {
		newCtx, ok := IsMTlsConnection(agentCall("agent1", map[string]string{"X-Forwarded-For": "192.168.1.2, 10.0.0.2"}))

		Expect(ok).To(BeTrue())
		username, _, ok := contexes.RetrieveUserInfo(newCtx)
		Expect(ok).To(BeTrue())
		Expect(username).To(Equal("192.168.1.2-agent1"))
	})
	It("should refuse context whose client headers claim mtls", func() {
		ctx := rpctest.ServerContext(context.Background(), map[string]string{"is_mtls": "true", "username": "admin"})
		_, ok := IsMTlsConnection(ctx)
		Expect(ok).To(BeFalse())
	})
	It("should accept again a context it already accepted", func() {
		accepted, ok := IsMTlsConnection(agentCall("agent1", nil))
		Expect(ok).To(BeTrue())
		_, ok = IsMTlsConnection(accepted)
		Expect(ok).To(BeTrue())
	})
	It("should keep the agent call headers", func() {
		accepted, _ := IsMTlsConnection(agentCall("agent1", map[string]string{"x-custom": "v"}))
		callInfo, _ := connect.CallInfoForServerContext(accepted)
		Expect(callInfo.RequestHeader().Get("x-custom")).To(Equal("v"))
	})
	It("should refuse context without peer", func() {
		_, ok := IsMTlsConnection(context.Background())
		Expect(ok).To(BeFalse())
	})
	It("should refuse peer without tls", func() {
		ctx, err := rpctest.ServerContextOverHTTP(nil, nil, nil)
		Expect(err).ToNot(HaveOccurred())

		_, ok := IsMTlsConnection(ctx)

		Expect(ok).To(BeFalse())
	})
	It("should refuse tls peer without client certificate", func() {
		_, ok := IsMTlsConnection(agentCall("", nil))

		Expect(ok).To(BeFalse())
	})
	When("test mode is enabled", func() {
		BeforeEach(func() {
			SetTestMode(true)
			DeferCleanup(SetTestMode, false)
		})
		It("should accept any connection as an agent", func() {
			newCtx, ok := IsMTlsConnection(context.Background())

			Expect(ok).To(BeTrue())
			username, groups, ok := contexes.RetrieveUserInfo(newCtx)
			Expect(ok).To(BeTrue())
			Expect(username).To(Equal("-test"))
			Expect(groups).To(BeEmpty())
		})
		It("should give no common name without a client certificate", func() {
			_, ok := ClientCommonName(context.Background())

			Expect(ok).To(BeFalse())
		})
	})
})

var _ = Describe("ClientCommonName", func() {
	It("should give the common name of the client certificate", func() {
		serverTLS, clientTLS := rpctest.NewTLS("edge-1")
		ctx, err := rpctest.ServerContextOverHTTP(serverTLS, clientTLS, nil)
		Expect(err).ToNot(HaveOccurred())

		commonName, ok := ClientCommonName(ctx)

		Expect(ok).To(BeTrue())
		Expect(commonName).To(Equal("edge-1"))
	})
	It("should give nothing to a client without certificate", func() {
		serverTLS, clientTLS := rpctest.NewTLS("")
		ctx, err := rpctest.ServerContextOverHTTP(serverTLS, clientTLS, nil)
		Expect(err).ToNot(HaveOccurred())

		_, ok := ClientCommonName(ctx)

		Expect(ok).To(BeFalse())
	})
})
