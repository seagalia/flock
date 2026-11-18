package handlers_test

import (
	"context"
	"maps"
	"net"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	svcregister "github.com/seagalia/flock/sdk/starling_waf/services/register/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	wafclient "github.com/seagalia/flock/agent/starling/waf/client"
)

// fakeWaf records the configs the agent gives the WAF, by identifier.
type fakeWaf struct {
	svcregister.UnimplementedRegisterServer
	mutex   sync.Mutex
	configs map[string]*svcregister.RegisterWafConfigRequest
}

func (f *fakeWaf) RegisterWafConfig(_ context.Context, req *svcregister.RegisterWafConfigRequest) (*emptypb.Empty, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.configs[req.GetIdentifier()] = req
	return &emptypb.Empty{}, nil
}

func (f *fakeWaf) UnregisterWafConfig(_ context.Context, req *svcregister.UnregisterWafConfigRequest) (*emptypb.Empty, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	delete(f.configs, req.GetIdentifier())
	return &emptypb.Empty{}, nil
}

func (f *fakeWaf) Configs() map[string]*svcregister.RegisterWafConfigRequest {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	configs := make(map[string]*svcregister.RegisterWafConfigRequest, len(f.configs))
	maps.Copy(configs, f.configs)
	return configs
}

// startFakeWaf serves the fake on a unix socket for the time of the spec and gives a client of it.
func startFakeWaf() (*fakeWaf, *wafclient.Client) {
	GinkgoHelper()
	// macOS refuses unix socket paths over 104 bytes, and the one of GinkgoT().TempDir() is longer
	dir, err := os.MkdirTemp("", "waf")
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(os.RemoveAll, dir)
	socketPath := filepath.Join(dir, "waf.sock")

	lis, err := net.Listen("unix", socketPath)
	Expect(err).ToNot(HaveOccurred())
	fake := &fakeWaf{configs: make(map[string]*svcregister.RegisterWafConfigRequest)}
	server := grpc.NewServer()
	svcregister.RegisterRegisterServer(server, fake)
	go server.Serve(lis) //nolint:errcheck
	DeferCleanup(server.Stop)

	client, err := wafclient.NewClient(socketPath)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(client.Close)
	return fake, client
}
