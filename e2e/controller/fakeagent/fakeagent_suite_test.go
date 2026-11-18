//go:build e2e

package fakeagent_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/controller/announcers"
	"github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/controller/lockers"
	"github.com/seagalia/flock/controller/permissioners"
	"github.com/seagalia/flock/controller/serves"
	"github.com/seagalia/flock/e2e/controller/env"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	"github.com/seagalia/flock/sdk/logging"
	"github.com/seagalia/flock/sdk/observe"
	"github.com/seagalia/flock/sdk/rpcserver"
	"github.com/seagalia/flock/sdk/rpcserver/rpctest"
	"github.com/seagalia/flock/sdk/storers"
)

func TestFakesubscriber(t *testing.T) {
	RegisterFailHandler(Fail)
	// logs of controller and the aggregator only show for failed specs
	if err := logging.Setup(logging.Options{Console: GinkgoWriter}); err != nil {
		t.Fatal(err)
	}
	RunSpecs(t, "E2E fakeagent Suite")
}

var certs *env.Certs

var _ = BeforeSuite(func() {
	var err error
	certs, err = env.NewCerts()
	Expect(err).NotTo(HaveOccurred())
})

// inProcessController is the discovery side of controller, wired like app.App does it
type inProcessController struct {
	addr     string
	storeMan storers.StoreManager
}

func startController() *inProcessController {
	auth.SetTestMode(false)
	storeMan := storers.NewMemoryManager()
	ctx, cancel := context.WithCancel(context.Background())
	server := serves.NewFlockServer(
		ctx, nil, config.AdminConfig{}, announcers.NewLink(context.Background(), storeMan), storeMan,
		nil, nil, permissioners.NewNull(), subscriberInstantiater(), nil, lockers.NewMemory(), nil,
	)

	// the discovery handler checks the mtls of the agent itself, the auth of user calls is not wired
	connectServer := rpcserver.NewServer(func(ctx context.Context, _ string) (context.Context, error) {
		return ctx, nil
	})
	svcdiscoveryconnect.RegisterAggregatedDiscoveryHandler(connectServer, server)
	addr, stop := rpctest.Serve(connectServer, controllerServerTLSConfig())
	DeferCleanup(func() {
		cancel()
		stop()
		observe.OffAll()
	})
	return &inProcessController{addr: addr, storeMan: storeMan}
}

// subscriberInstantiater declares the agent of certs.SubscriberTLSConfig for every instance
func subscriberInstantiater() *instantiaters.Static {
	return instantiaters.NewStatic([]*config.AgentInstantiator{{
		UUID: env.SubscriberUuid, Name: env.SubscriberName, Host: "127.0.0.1:1",
	}})
}

// controllerServerTLSConfig is how app/server.go listens: client certificates are optional, agents present one
func controllerServerTLSConfig() *tls.Config {
	controllerCert, err := tls.X509KeyPair(certs.ControllerServerCertPem, certs.ControllerServerKeyPem)
	Expect(err).NotTo(HaveOccurred())
	caPool := x509.NewCertPool()
	Expect(caPool.AppendCertsFromPEM(certs.CAPem)).To(BeTrue())
	return &tls.Config{
		Certificates: []tls.Certificate{controllerCert},
		ClientAuth:   tls.VerifyClientCertIfGiven,
		ClientCAs:    caPool,
	}
}
