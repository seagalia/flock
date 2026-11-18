package helpers_test

import (
	"context"
	"net"
	"time"

	"connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/rpcserver"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
)

var _ = Describe("GrpcClients", func() {
	isReachable := func(clients *GrpcClients, addr string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return clients.IsReachable(ctx, addr)
	}

	It("tells a server is reachable over TLS", func() {
		serverTLS, clientTLS := rpctest.NewTLS("client")
		addr, stop := rpctest.Serve(connect.NewServer(), serverTLS)
		DeferCleanup(stop)
		clients := NewGrpcClients(clientTLS)
		DeferCleanup(clients.Close)

		Expect(isReachable(clients, addr)).To(BeTrue())
	})

	It("tells a server refusing the call is reachable, as a ready grpc-go connection was", func() {
		refusing := rpcserver.NewServer(func(context.Context, string) (context.Context, error) {
			return nil, connect.NewError(connect.CodeUnauthenticated, "no token")
		})
		addr, stop := rpctest.Serve(refusing, nil)
		DeferCleanup(stop)
		clients := NewGrpcClients(nil)
		DeferCleanup(clients.Close)

		Expect(isReachable(clients, addr)).To(BeTrue())
	})

	It("tells nothing listening is not reachable", func() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).ToNot(HaveOccurred())
		addr := listener.Addr().String()
		Expect(listener.Close()).To(Succeed())
		clients := NewGrpcClients(nil)
		DeferCleanup(clients.Close)

		Expect(isReachable(clients, addr)).To(BeFalse())
	})

	It("tells a server with an untrusted certificate is not reachable", func() {
		serverTLS, _ := rpctest.NewTLS("")
		_, otherCAClientTLS := rpctest.NewTLS("")
		addr, stop := rpctest.Serve(connect.NewServer(), serverTLS)
		DeferCleanup(stop)
		clients := NewGrpcClients(otherCAClientTLS)
		DeferCleanup(clients.Close)

		Expect(isReachable(clients, addr)).To(BeFalse())
	})
})
