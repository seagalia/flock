package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/seagalia/flock/agent/starling/agentserv"
	"github.com/seagalia/flock/sdk/agentsdk/loghooks"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/rpcserver"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
)

var _ = Describe("Server helpers", func() {
	// the grpc-go server of envoy's xDS gives its peer in the context with grpc's peer package
	Context("simpleAuth of a grpc-go call", func() {
		withPeer := func(addr string, authInfo credentials.AuthInfo) context.Context {
			tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
			Expect(err).ToNot(HaveOccurred())
			return peer.NewContext(context.Background(), &peer.Peer{Addr: tcpAddr, AuthInfo: authInfo})
		}

		It("should reject a request without peer", func() {
			_, err := simpleAuth(context.Background(), "/test.Service/Method")
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated))
		})

		It("should accept localhost without certificate", func() {
			_, err := simpleAuth(withPeer("127.0.0.1:5000", nil), "/test.Service/Method")
			Expect(err).ToNot(HaveOccurred())
		})

		It("should reject remote peer without client certificate", func() {
			_, err := simpleAuth(withPeer("10.0.0.1:5000", nil), "/test.Service/Method")
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated))

			_, err = simpleAuth(withPeer("10.0.0.1:5000", credentials.TLSInfo{State: tls.ConnectionState{}}), "/test.Service/Method")
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated))
		})

		It("should accept remote peer with client certificate", func() {
			authInfo := credentials.TLSInfo{State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}}}
			_, err := simpleAuth(withPeer("10.0.0.1:5000", authInfo), "/test.Service/Method")
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("simpleAuth of a connect call", func() {
		const procedure = "/flock.services.agent.status.v1.AgentStatus/GetAgentStatus"

		It("should reject remote peer without client certificate", func() {
			ctx := rpctest.ServerContextWithInfo(context.Background(), &connect.CallInfo{PeerAddr: "10.0.0.1:5000"})
			_, err := simpleAuth(ctx, procedure)
			Expect(connect.CodeOf(err)).To(Equal(connect.CodeUnauthenticated))
		})

		It("should accept localhost without certificate", func() {
			ctx, err := rpctest.ServerContextOverHTTP(nil, nil, nil)
			Expect(err).ToNot(HaveOccurred())
			_, err = simpleAuth(ctx, procedure)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should accept peer with client certificate", func() {
			serverTLS, clientTLS := rpctest.NewTLS("controller")
			ctx, err := rpctest.ServerContextOverHTTP(serverTLS, clientTLS, nil)
			Expect(err).ToNot(HaveOccurred())
			_, err = simpleAuth(ctx, procedure)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})

var _ = Describe("serverAddrs", func() {
	It("should listen on the exposed host and on loopback", func() {
		Expect(serverAddrs("10.0.0.1", 18000)).To(Equal([]string{"10.0.0.1:18000", "127.0.0.1:18000"}))
	})

	It("should listen once when the exposed host is loopback", func() {
		Expect(serverAddrs("127.0.0.1", 18000)).To(Equal([]string{"127.0.0.1:18000"}))
	})
})

var _ = Describe("serveHttp", func() {
	It("should stop within the drain grace while a Follow client stays connected", func() {
		setDrainGrace(200 * time.Millisecond)
		serverTLS, clientTLS := rpctest.NewTLS("controller")
		app := &App{entry: logging.Component("app"), tlsConf: serverTLS}
		agentServer := agentserv.NewAgentServer(
			"agent", nil, loghooks.NewListenerWriter(), agentserv.TlsInfo{}, nil, nil, nil, nil, nil, nil, nil,
		)
		connectServer := rpcserver.NewServer(simpleAuth)
		agentlogsconnect.RegisterLogsHandler(connectServer, agentServer)
		mux := http.NewServeMux()
		rpcserver.Mount(mux, connectServer, grpchealth.NewStaticChecker())
		listeners, err := listenAll([]string{"127.0.0.1:0"})
		Expect(err).ToNot(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		served := make(chan error, 1)
		go func() {
			served <- app.serveHttp(ctx, listeners, mux, make(chan struct{}, 1))
		}()

		clients := helpers.NewGrpcClients(clientTLS)
		DeferCleanup(clients.Close)
		logsClient := agentlogsconnect.NewLogsClient(clients.Client(listeners[0].Addr().String()))
		stream, err := logsClient.Follow(context.Background(), &emptypb.Empty{})
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(stream.Close)
		first, err := stream.Receive()
		Expect(err).ToNot(HaveOccurred())
		Expect(first.GetStarted()).ToNot(BeNil())

		cancel()
		// the stream holds Shutdown until the grace cancels its ctx, well before shutdownTimeout
		Eventually(served, 5*time.Second).Should(Receive(Succeed()))
	})
})
