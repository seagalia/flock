//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/seagalia/flock/e2e/controller/env"
	"github.com/seagalia/flock/e2e/controller/fakeagent"
)

var e2eEnv *env.Env

// what the current spec started, dumped when it fails
var (
	specControllers []*env.Controller
	specSubscribers map[string]*fakeagent.Subscriber
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(30 * time.Second)
	SetDefaultEventuallyPollingInterval(200 * time.Millisecond)
	RunSpecs(t, "flock-controller e2e Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	var err error
	e2eEnv, err = env.Setup(ctx)
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func(ctx SpecContext) {
		e2eEnv.Teardown(ctx)
	}, NodeTimeout(2*time.Minute))
}, NodeTimeout(15*time.Minute))

var _ = BeforeEach(func() {
	specControllers = nil
	specSubscribers = make(map[string]*fakeagent.Subscriber)
})

// registerControllerForDump is called again by every spec of an Ordered container sharing one controller
func registerControllerForDump(g *env.Controller) {
	if slices.Contains(specControllers, g) {
		return
	}
	specControllers = append(specControllers, g)
}

// registerSubscriberForDump keeps every agent when several connect for the same instance
func registerSubscriberForDump(name string, m *fakeagent.Subscriber) {
	if _, taken := specSubscribers[name]; taken {
		name = fmt.Sprintf("%s #%d", name, len(specSubscribers)+1)
	}
	specSubscribers[name] = m
}

// startController removes the container at the end of the spec, so the next spec can take the same name on the network
func startController(ctx context.Context, o env.ControllerOptions) *env.Controller {
	GinkgoHelper()
	g, err := e2eEnv.StartController(ctx, o)
	Expect(err).NotTo(HaveOccurred())
	registerControllerForDump(g)
	DeferCleanup(func(ctx SpecContext) {
		_ = g.Terminate(ctx)
	}, NodeTimeout(time.Minute))
	return g
}

// loginAdmin uses a break-glass token, so it works whatever the login service of g
func loginAdmin(ctx context.Context, g *env.Controller) *env.Api {
	GinkgoHelper()
	api, err := g.LoginAsBreakGlass(ctx, "e2e admin")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(api.Close)
	return api
}

func loginAs(ctx context.Context, g *env.Controller, username, password string) *env.Api {
	GinkgoHelper()
	api, err := g.LoginAs(ctx, username, password)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(api.Close)
	return api
}

// loginOidc calls g with an ID token of the provider for one of env.Oauth2Users
func loginOidc(ctx context.Context, g *env.Controller, provider *env.Oauth2, username string) *env.Api {
	GinkgoHelper()
	idToken, err := provider.IdToken(ctx, username, env.Oauth2Users[username])
	Expect(err).NotTo(HaveOccurred())
	api, err := g.LoginWithBearer(ctx, idToken)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(api.Close)
	return api
}

// connectSubscriber keeps the agent streams open until the end of the spec, not only for ctx
func connectSubscriber(ctx context.Context, g *env.Controller, instanceUuid string) *fakeagent.Subscriber {
	GinkgoHelper()
	m := fakeagent.New(instanceUuid, e2eEnv.Certs.SubscriberTLSConfig(env.HostServerName))
	registerSubscriberForDump(instanceUuid, m)
	subscriberCtx, cancel := context.WithCancel(context.Background())
	DeferCleanup(func() {
		m.Drop()
		cancel()
	})
	connectCtx, cancelConnect := context.WithTimeout(ctx, 30*time.Second)
	defer cancelConnect()
	connected := make(chan error, 1)
	go func() { connected <- m.Connect(subscriberCtx, g.Addr(connectCtx)) }()
	select {
	case err := <-connected:
		Expect(err).NotTo(HaveOccurred())
	case <-connectCtx.Done():
		Fail(fmt.Sprintf("agent %s did not connect to %s in time", instanceUuid, g.Name()))
	}
	return m
}

var _ = JustAfterEach(func() {
	if !CurrentSpecReport().Failed() {
		return
	}
	dumpCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, g := range specControllers {
		AddReportEntry("controller "+g.Name()+" logs", g.Logs(dumpCtx), types.ReportEntryVisibilityFailureOrVerbose)
	}
	for name, m := range specSubscribers {
		AddReportEntry("agent "+name+" history", subscriberHistory(m), types.ReportEntryVisibilityFailureOrVerbose)
	}
	// keeps containers alive to inspect them with docker exec before cleanup
	pause := os.Getenv("E2E_PAUSE_ON_FAILURE")
	if pause == "" {
		return
	}
	duration, err := time.ParseDuration(pause)
	if err != nil {
		duration = 10 * time.Minute
	}
	for _, g := range specControllers {
		GinkgoWriter.Printf("E2E_PAUSE_ON_FAILURE: controller %s is container %s\n", g.Name(), g.Container.GetContainerID())
	}
	GinkgoWriter.Printf("E2E_PAUSE_ON_FAILURE: pausing %s\n", duration)
	time.Sleep(duration)
})

func subscriberHistory(m *fakeagent.Subscriber) string {
	var out strings.Builder
	fmt.Fprintf(&out, "last error: %v\n", m.Err())
	for _, typeUrl := range fakeagent.SubscriberTypeUrls {
		fmt.Fprintf(&out, "== %s\n", typeUrl)
		for _, resp := range m.History(typeUrl) {
			b, err := protojson.Marshal(resp)
			if err != nil {
				fmt.Fprintf(&out, "unprintable response: %v\n", err)
				continue
			}
			out.Write(b)
			out.WriteString("\n")
		}
	}
	return out.String()
}
