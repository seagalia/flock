package client_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/agent/starling/waf/client"
	svcregister "github.com/seagalia/flock/sdk/starling_waf/services/register/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fakeRegister struct {
	svcregister.UnimplementedRegisterServer
	registered chan *svcregister.RegisterWafConfigRequest
}

func (f *fakeRegister) RegisterWafConfig(_ context.Context, req *svcregister.RegisterWafConfigRequest) (*emptypb.Empty, error) {
	f.registered <- req
	return &emptypb.Empty{}, nil
}

var _ = Describe("Client", func() {
	var socketPath string

	serveFake := func() *fakeRegister {
		lis, err := net.Listen("unix", socketPath)
		Expect(err).ToNot(HaveOccurred())
		fake := &fakeRegister{registered: make(chan *svcregister.RegisterWafConfigRequest, 1)}
		server := grpc.NewServer()
		svcregister.RegisterRegisterServer(server, fake)
		go server.Serve(lis) //nolint:errcheck
		DeferCleanup(server.Stop)
		return fake
	}
	register := func(ctx context.Context, wafClient *client.Client) error {
		_, err := wafClient.RegisterWafConfig(ctx, &svcregister.RegisterWafConfigRequest{
			Identifier: "vs-1",
			Config:     &svcregister.RegisterWafConfigRequest_DetectOnly{DetectOnly: true},
		})
		return err
	}

	BeforeEach(func() {
		// macOS refuses unix socket paths over 104 bytes, and the one of GinkgoT().TempDir() is longer
		dir, err := os.MkdirTemp("", "waf")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		socketPath = filepath.Join(dir, "waf.sock")
	})

	It("calls the Register service over the unix socket", func() {
		fake := serveFake()
		wafClient, err := client.NewClient(socketPath)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(wafClient.Close)

		Expect(register(context.Background(), wafClient)).To(Succeed())
		Expect(<-fake.registered).To(HaveField("Identifier", "vs-1"))
	})

	It("waits for a WAF starting after the call", func() {
		wafClient, err := client.NewClient(socketPath)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(wafClient.Close)

		registered := make(chan error, 1)
		go func() {
			registered <- register(context.Background(), wafClient)
		}()
		Consistently(registered, 200*time.Millisecond).ShouldNot(Receive())

		fake := serveFake()
		Eventually(registered, 5*time.Second).Should(Receive(BeNil()))
		Expect(<-fake.registered).To(HaveField("Identifier", "vs-1"))
	})

	It("gives up when the context ends before the WAF starts", func() {
		wafClient, err := client.NewClient(socketPath)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(wafClient.Close)

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		Expect(register(ctx, wafClient)).To(HaveOccurred())
	})
})
