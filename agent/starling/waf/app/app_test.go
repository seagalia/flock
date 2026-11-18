package app_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	extProcPb "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/agent/starling/waf/app"
	"github.com/seagalia/flock/agent/starling/waf/client"
	"github.com/seagalia/flock/agent/starling/waf/wafproccessor"
	svcregister "github.com/seagalia/flock/sdk/starling_waf/services/register/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var _ = Describe("App", func() {
	var socketPath, metricsSocketPath string

	BeforeEach(func() {
		// macOS refuses unix socket paths over 104 bytes, and the one of GinkgoT().TempDir() is longer
		dir, err := os.MkdirTemp("", "waf")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		socketPath = filepath.Join(dir, "waf.sock")
		metricsSocketPath = filepath.Join(dir, "metrics.sock")
	})

	It("serves ext_proc, Register and the metrics on their sockets until its context ends", func() {
		// a socket left by a crashed run must not prevent the start
		Expect(os.WriteFile(socketPath, nil, 0o600)).To(Succeed())
		wafApp, err := app.NewApp(socketPath, metricsSocketPath, 1<<30)
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		runErr := make(chan error, 1)
		go func() {
			runErr <- wafApp.RunWithContext(ctx)
		}()
		Eventually(func() os.FileMode {
			info, _ := os.Stat(socketPath)
			if info == nil {
				return 0
			}
			return info.Mode().Type()
		}, 10*time.Second).Should(Equal(os.ModeSocket))

		wafClient, err := client.NewClient(socketPath)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(wafClient.Close)
		_, err = wafClient.RegisterWafConfig(context.Background(), &svcregister.RegisterWafConfigRequest{
			Identifier: "vs-1",
			Config:     &svcregister.RegisterWafConfigRequest_DetectOnly{DetectOnly: true},
		})
		Expect(err).ToNot(HaveOccurred())
		_, err = wafClient.RegisterWafConfig(context.Background(), &svcregister.RegisterWafConfigRequest{
			Config: &svcregister.RegisterWafConfigRequest_DetectOnly{DetectOnly: true},
		})
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))

		// an unknown WAF fails the stream in the processor, an unregistered service would answer Unimplemented
		conn, err := grpc.NewClient("unix:"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(conn.Close)
		streamCtx := metadata.AppendToOutgoingContext(context.Background(), wafproccessor.MetadataKeyWafUUID, "unknown")
		stream, err := extProcPb.NewExternalProcessorClient(conn).Process(streamCtx)
		Expect(err).ToNot(HaveOccurred())
		_, err = stream.Recv()
		Expect(status.Code(err)).To(Equal(codes.Internal))

		metricsClient := &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", metricsSocketPath)
			},
		}}
		resp, err := metricsClient.Get("http://waf/metrics")
		Expect(err).ToNot(HaveOccurred())
		metrics, err := io.ReadAll(resp.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Body.Close()).To(Succeed())
		Expect(string(metrics)).To(ContainSubstring(
			`starling_waf_grpc_server_handled_total{grpc_code="OK",grpc_method="RegisterWafConfig"`))

		cancel()
		Eventually(runErr, 10*time.Second).Should(Receive(BeNil()))
		Expect(socketPath).ToNot(BeAnExistingFile())
		Expect(metricsSocketPath).ToNot(BeAnExistingFile())
	})

	It("closes the streams still open once the graceful stop waited too long", func() {
		DeferCleanup(app.SetGracefulStopTimeout(200 * time.Millisecond))
		wafApp, err := app.NewApp(socketPath, "", 1<<30)
		Expect(err).ToNot(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		runErr := make(chan error, 1)
		go func() {
			runErr <- wafApp.RunWithContext(ctx)
		}()
		wafClient, err := client.NewClient(socketPath)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(wafClient.Close)
		Eventually(func() error {
			_, err := wafClient.RegisterWafConfig(context.Background(), &svcregister.RegisterWafConfigRequest{
				Identifier: "vs-1",
				Config:     &svcregister.RegisterWafConfigRequest_DetectOnly{DetectOnly: true},
			})
			return err
		}, 10*time.Second).Should(Succeed())

		// a stream envoy holds open, the WAF waits for its first message
		conn, err := grpc.NewClient("unix:"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(conn.Close)
		streamCtx := metadata.AppendToOutgoingContext(context.Background(), wafproccessor.MetadataKeyWafUUID, "vs-1")
		stream, err := extProcPb.NewExternalProcessorClient(conn).Process(streamCtx)
		Expect(err).ToNot(HaveOccurred())
		recvErr := make(chan error, 1)
		go func() {
			_, err := stream.Recv()
			recvErr <- err
		}()
		Consistently(recvErr, 300*time.Millisecond).ShouldNot(Receive())

		cancel()
		Eventually(runErr, 5*time.Second).Should(Receive(BeNil()))
		Eventually(recvErr, 5*time.Second).Should(Receive(HaveOccurred()))
	})

	It("fails when it can't listen on the socket", func() {
		wafApp, err := app.NewApp(filepath.Join(socketPath, "missing-dir", "waf.sock"), "", 1<<30)
		Expect(err).ToNot(HaveOccurred())

		err = wafApp.RunWithContext(context.Background())
		Expect(err).To(MatchError(ContainSubstring("listen")))
	})
})
