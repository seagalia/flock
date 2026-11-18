package wafproccessor

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

var (
	procServer    *EnvoyProcServer
	extProcClient extProcPb.ExternalProcessorClient
)

func TestWafproccessor(t *testing.T) {
	RegisterFailHandler(Fail)
	if err := logging.Setup(logging.Options{Console: GinkgoWriter}); err != nil {
		t.Fatal(err)
	}
	RunSpecs(t, "Wafproccessor Suite")
}

// The CRS WAFs take seconds to compile, every spec shares one server.
var _ = BeforeSuite(func() {
	var err error
	procServer, err = NewEnvoyProcServer(1 << 30)
	Expect(err).ToNot(HaveOccurred())

	// macOS refuses unix socket paths over 104 bytes, and the one of GinkgoT().TempDir() is longer
	dir, err := os.MkdirTemp("", "waf")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(os.RemoveAll, dir)
	socketPath := filepath.Join(dir, "waf.sock")
	lis, err := net.Listen("unix", socketPath)
	Expect(err).ToNot(HaveOccurred())
	server := grpc.NewServer(grpc.MaxRecvMsgSize(MaxMessageSize))
	extProcPb.RegisterExternalProcessorServer(server, procServer)
	go server.Serve(lis) //nolint:errcheck
	DeferCleanup(server.Stop)

	conn, err := grpc.NewClient("unix:"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(conn.Close)
	extProcClient = extProcPb.NewExternalProcessorClient(conn)
})

// exchange is one ext_proc stream, the messages envoy sends for one HTTP request.
type exchange struct {
	stream grpc.BidiStreamingClient[extProcPb.ProcessingRequest, extProcPb.ProcessingResponse]
	// the last override_message_timeout the server sent, which envoy reads as a message of its own
	overriddenTimeout time.Duration
}

// openExchange uses the default WAF when wafID is empty, like a listener with no x-waf-uuid metadata.
func openExchange(wafID string) *exchange {
	GinkgoHelper()
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	if wafID != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, MetadataKeyWafUUID, wafID)
	}
	stream, err := extProcClient.Process(ctx)
	Expect(err).ToNot(HaveOccurred())
	// the server counts a transaction when its stream ends: waiting for that end keeps the count of this spec's
	// requests out of the next specs' counters
	DeferCleanup(func() {
		_ = stream.CloseSend()
		for {
			if _, err := stream.Recv(); err != nil {
				return
			}
		}
	})
	return &exchange{stream: stream}
}

func (e *exchange) send(req *extProcPb.ProcessingRequest) (*extProcPb.ProcessingResponse, error) {
	err := e.stream.Send(req)
	// a stream the server already ended gives io.EOF on Send, its status only comes with Recv
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	for {
		resp, err := e.stream.Recv()
		if err != nil || resp.GetOverrideMessageTimeout() == nil {
			return resp, err
		}
		e.overriddenTimeout = resp.GetOverrideMessageTimeout().AsDuration()
	}
}

func (e *exchange) mustSend(req *extProcPb.ProcessingRequest) *extProcPb.ProcessingResponse {
	GinkgoHelper()
	resp, err := e.send(req)
	Expect(err).ToNot(HaveOccurred())
	return resp
}

func immediateStatus(resp *extProcPb.ProcessingResponse) int {
	return int(resp.GetImmediateResponse().GetStatus().GetCode())
}
