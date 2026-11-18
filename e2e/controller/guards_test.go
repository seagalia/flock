//go:build e2e

package e2e_test

import (
	"context"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"

	"github.com/seagalia/flock/e2e/controller/env"
	"github.com/seagalia/flock/e2e/controller/fakeagent"
)

var _ = Describe("Guards", Ordered, func() {
	var (
		g     *env.Controller
		admin *env.Api
	)

	BeforeAll(func(ctx SpecContext) {
		g = startController(ctx, env.ControllerOptions{
			Name: "controller-guards", Store: env.StoreSqlite, Instantiators: env.SubscriberInstantiators(),
		})
		admin = loginAdmin(ctx, g)
	}, NodeTimeout(10*time.Minute))

	BeforeEach(func() {
		registerControllerForDump(g)
	})

	It("refuses a stream without client certificate", func(ctx SpecContext) {
		zone := subscriberZone(ctx, admin)
		vsUuid, _ := createTcpVs(ctx, admin, 8001)
		instanceUuid, _ := createInstance(ctx, admin, zone, vsUuid)

		certless := fakeagent.New(instanceUuid, e2eEnv.Certs.UserTLSConfig(env.HostServerName))
		registerSubscriberForDump(instanceUuid, certless)
		DeferCleanup(certless.Drop)
		Expect(certless.Connect(ctx, g.Addr(ctx), vipType)).To(Succeed())
		// without a token the auth interceptor refuses before the discovery handler sees the stream
		Eventually(func() connect.Code { return connect.CodeOf(certless.Err()) }).Should(Equal(connect.CodeUnauthenticated))
		Expect(certless.State(vipType)).To(BeEmpty())

		By("opening it as a logged in user, still without client certificate")
		recvErr := firstDiscoveryRecvErr(ctx, admin.Conn, instanceUuid)
		Expect(connect.CodeOf(recvErr)).To(Equal(connect.CodePermissionDenied), "%v", recvErr)
	}, NodeTimeout(2*time.Minute))

	It("refuses a stream whose certificate names no declared agent", func(ctx SpecContext) {
		zone := subscriberZone(ctx, admin)
		vsUuid, _ := createTcpVs(ctx, admin, 8001)
		instanceUuid, _ := createInstance(ctx, admin, zone, vsUuid)
		undeclaredTLS, err := e2eEnv.Certs.AgentClientTLSConfig("agent-undeclared", env.HostServerName)
		Expect(err).NotTo(HaveOccurred())

		undeclared := fakeagent.New(instanceUuid, undeclaredTLS)
		registerSubscriberForDump(instanceUuid, undeclared)
		DeferCleanup(undeclared.Drop)
		Expect(undeclared.Connect(ctx, g.Addr(ctx), vipType)).To(Succeed())

		Eventually(func() connect.Code { return connect.CodeOf(undeclared.Err()) }).Should(Equal(connect.CodePermissionDenied))
		Expect(undeclared.Err()).To(MatchError(ContainSubstring(`"agent-undeclared"`)))
		Expect(undeclared.State(vipType)).To(BeEmpty())

		By("connecting with the declared certificate for the same instance")
		declared := connectSubscriber(ctx, g, instanceUuid)
		Consistently(declared.Err, 2*time.Second).ShouldNot(HaveOccurred())
	}, NodeTimeout(2*time.Minute))

	It("refuses a stream for an unknown instance", func(ctx SpecContext) {
		unknownUuid := env.Unique("unknown-instance")
		stranger := fakeagent.New(unknownUuid, e2eEnv.Certs.SubscriberTLSConfig(env.HostServerName))
		registerSubscriberForDump(unknownUuid, stranger)
		DeferCleanup(stranger.Drop)

		Expect(stranger.Connect(ctx, g.Addr(ctx), vipType)).To(Succeed())
		Eventually(func() connect.Code { return connect.CodeOf(stranger.Err()) }).Should(Equal(connect.CodeNotFound))
	}, NodeTimeout(2*time.Minute))

	It("refuses a call claiming mtls through headers", func(ctx SpecContext) {
		zone := subscriberZone(ctx, admin)
		vsUuid, _ := createTcpVs(ctx, admin, 8001)
		instanceUuid, _ := createInstance(ctx, admin, zone, vsUuid)
		anonymous, err := g.Anonymous(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(anonymous.Close)
		// identity travels in context values set by controller, these keys must give nothing
		claimingCtx, callInfo := connect.NewClientContext(ctx)
		callInfo.RequestHeader().Set("is_mtls", "true")
		callInfo.RequestHeader().Set("username", "admin")

		_, err = anonymous.Zone.CreateZone(claimingCtx, &svczone.CreateZoneRequest{Zone: env.NewZone(env.Unique("zone"))})
		Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated), "%v", err)

		recvErr := firstDiscoveryRecvErr(claimingCtx, anonymous.Conn, instanceUuid)
		Expect(connect.CodeOf(recvErr)).To(Equal(connect.CodeUnauthenticated), "%v", recvErr)
	}, NodeTimeout(2*time.Minute))
})

// firstDiscoveryRecvErr opens a discovery stream like an agent does and gives the error controller ends it with
func firstDiscoveryRecvErr(ctx context.Context, conn *connect.Client, instanceUuid string) error {
	GinkgoHelper()
	streamCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stream, err := svcdiscoveryconnect.NewAggregatedDiscoveryClient(conn).DeltaAggregatedResources(streamCtx)
	if err != nil {
		return err
	}
	defer stream.Close() // nolint:errcheck
	// a Send on a stream controller already ended gives io.EOF, its status only comes on Receive
	_ = stream.Send(&svcdiscovery.DeltaDiscoveryRequest{TypeUrl: vipType, InstanceUuid: instanceUuid})
	resp, err := stream.Receive()
	Expect(resp).To(BeNil(), "controller answered a stream it must refuse")
	return err
}
