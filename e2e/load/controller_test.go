//go:build load

package load_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	mathrand "math/rand/v2"
	"net/http"
	"slices"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	"github.com/onsi/ginkgo/v2/types"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/route/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/zone/v1"
	svcbackend "github.com/seagalia/flock/sdk/flock/services/backend/v1"
	svccert "github.com/seagalia/flock/sdk/flock/services/cert/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	svczone "github.com/seagalia/flock/sdk/flock/services/zone/v1"
	"github.com/seagalia/flock/sdk/helpers"

	ctlenv "github.com/seagalia/flock/e2e/controller/env"
	"github.com/seagalia/flock/e2e/controller/fakeagent"
	"github.com/seagalia/flock/e2e/load/measure"
)

const seedWorkers = 8

var _ = Describe("controller", Ordered, Label("controller"), func() {
	vsType := helpers.TypeUrl[*vs.VirtualServer]()
	table := &measure.Table{Title: "controller", ContainerNote: "container = controller process + sqlite page cache"}
	var (
		e2eEnv     *ctlenv.Env
		controller *ctlenv.Controller
		admin      *ctlenv.Api
		target     measured
		// filled by the seeding, index i is resource i
		vipUuids      []string
		instanceUuids []string
		// wantVs is how many virtual servers each instance must hold
		wantVs        map[string]int
		subscribers   map[string]*fakeagent.Subscriber
		subscriberCtx context.Context
	)

	addRow := func(phase string, sample measure.Sample) {
		table.Rows = append(table.Rows, measure.Row{Phase: phase, Sample: sample})
	}

	BeforeAll(func(ctx SpecContext) {
		DeferCleanup(func() { report.Tables = append(report.Tables, *table) })
		var err error
		e2eEnv, err = ctlenv.Setup(ctx)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) { e2eEnv.Teardown(ctx) }, NodeTimeout(2*time.Minute))

		controller, err = e2eEnv.StartController(ctx, ctlenv.ControllerOptions{
			Name: "controller-load", Store: ctlenv.StoreSqlite, Instantiators: ctlenv.SubscriberInstantiators(),
			// debug lines would allocate in the measured process, production runs at info
			LogLevel: "info",
		})
		Expect(err).NotTo(HaveOccurred())
		admin, err = controller.LoginAs(ctx, ctlenv.AdminUser, ctlenv.AdminPassword)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(admin.Close)

		target = measured{metrics: controllerMetrics(controller, e2eEnv.Certs), container: controller.Container}
		var cancel context.CancelFunc
		subscriberCtx, cancel = context.WithCancel(context.Background())
		DeferCleanup(cancel)
		addRow("idle", target.settled(ctx))
	}, NodeTimeout(15*time.Minute))

	JustAfterEach(func() {
		if !CurrentSpecReport().Failed() || controller == nil {
			return
		}
		dumpCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		AddReportEntry("controller logs", controller.Logs(dumpCtx), types.ReportEntryVisibilityFailureOrVerbose)
	})

	// the agents stay connected for every phase after the one connecting them
	AfterAll(func() {
		for _, subscriber := range subscribers {
			subscriber.Drop()
		}
	})

	It("seeds the store", func(ctx SpecContext) {
		certUuids := make([]string, ctlCerts)
		certPem, keyPem := selfSignedPem()
		parallel(ctlCerts, seedWorkers, func(i int) {
			resp, err := admin.Cert.CreateCertificate(ctx, &svccert.CreateCertificateRequest{Certificate: &cert.Certificate{
				Name: ctlenv.Unique("cert"), CertChain: certPem, PrivateKey: keyPem,
			}})
			Expect(err).NotTo(HaveOccurred())
			certUuids[i] = resp.GetCertificate().GetUUID()
		})

		backendUuids := make([]string, ctlBackends)
		parallel(ctlBackends, seedWorkers, func(i int) {
			backendUuids[i] = createBackend(ctx, admin)
		})

		// vs i is on vip i % ctlVips, the port keeps the virtual servers of one vip apart
		vsUuids := make([]string, ctlVs)
		parallel(ctlVs, seedWorkers, func(i int) {
			backendUuid := backendUuids[i%ctlBackends]
			port := uint32(10000 + i/ctlVips)
			virtualServer := ctlenv.NewTcpVs(ctlenv.Unique("vs"), port, backendUuid)
			isHttp := i%2 == 1
			if isHttp {
				vhost := ctlenv.NewTlsVirtualHost(certUuids[(i/2)%ctlCerts])
				vhost.Routes = []*route.Route{{
					Name:   "default",
					Match:  &route.RouteMatch{PathSpecifier: &route.RouteMatch_Prefix{Prefix: "/"}},
					Action: &route.Route_BackendUuid{BackendUuid: backendUuid},
				}}
				virtualServer = ctlenv.NewHttpVs(ctlenv.Unique("vs"), port, []*route.VirtualHost{vhost})
			}
			resp, err := admin.VS.CreateVS(ctx, &svcvs.CreateVSRequest{Vs: virtualServer})
			Expect(err).NotTo(HaveOccurred())
			vsUuids[i] = resp.GetVs().GetUUID()
		})

		zoneResp, err := admin.Zone.CreateZone(ctx, &svczone.CreateZoneRequest{Zone: &zone.Zone{
			Name:  ctlenv.SubscriberZone,
			Zone:  "z1",
			Pools: []*zone.IpPool{{Name: "default", Subnets: []*zone.Subnet{{Ip: "10.0.0.0", PrefixLength: 16}}}},
		}})
		Expect(err).NotTo(HaveOccurred())

		vipUuids = make([]string, ctlVips)
		vsCountOfVip := make([]int, ctlVips)
		parallel(ctlVips, seedWorkers, func(j int) {
			onVip := make([]string, 0)
			for i := j; i < ctlVs; i += ctlVips {
				onVip = append(onVip, vsUuids[i])
			}
			vsCountOfVip[j] = len(onVip)
			ip := fmt.Sprintf("10.0.%d.%d", j/250, j%250+1)
			resp, err := admin.VIP.CreateVIP(ctx, &svcvip.CreateVIPRequest{Vip: ctlenv.NewVip(ctlenv.Unique("vip"), ip, onVip...)})
			Expect(err).NotTo(HaveOccurred())
			vipUuids[j] = resp.GetVip().GetUUID()
		})

		// vip j is on instance j % ctlAgents
		instanceUuids = make([]string, ctlAgents)
		vsCountOfInstance := make([]int, ctlAgents)
		parallel(ctlAgents, seedWorkers, func(k int) {
			onInstance := make([]string, 0)
			for j := k; j < ctlVips; j += ctlAgents {
				onInstance = append(onInstance, vipUuids[j])
				vsCountOfInstance[k] += vsCountOfVip[j]
			}
			resp, err := admin.Instance.CreateInstance(ctx, &svcinstance.CreateInstanceRequest{
				Instance: ctlenv.NewInstance(ctlenv.Unique("instance"), zoneResp.GetZone().GetUUID(), onInstance...),
			})
			Expect(err).NotTo(HaveOccurred())
			instanceUuids[k] = resp.GetInstance().GetUUID()
		})
		wantVs = make(map[string]int, ctlAgents)
		for k, instanceUuid := range instanceUuids {
			wantVs[instanceUuid] = vsCountOfInstance[k]
		}

		addRow("seeded", target.settled(ctx))
	}, NodeTimeout(30*time.Minute))

	It("serves the agents", func(ctx SpecContext) {
		tlsConf := e2eEnv.Certs.SubscriberTLSConfig(ctlenv.HostServerName)
		subscribers = make(map[string]*fakeagent.Subscriber, ctlAgents)
		var mutex sync.Mutex
		parallel(ctlAgents, seedWorkers, func(k int) {
			subscriber := fakeagent.New(instanceUuids[k], tlsConf)
			Expect(subscriber.Connect(subscriberCtx, controller.Addr(ctx))).To(Succeed())
			mutex.Lock()
			subscribers[instanceUuids[k]] = subscriber
			mutex.Unlock()
		})

		waitSynced(subscribers, vsType, wantVs)
		addRow("agents connected", target.settled(ctx))
	}, NodeTimeout(15*time.Minute))

	It("serves concurrent api clients", func(ctx SpecContext) {
		deadline := time.Now().Add(ctlApiFor)
		var (
			wg       sync.WaitGroup
			mutex    sync.Mutex
			firstErr error
		)
		keepFirst := func(err error) {
			mutex.Lock()
			defer mutex.Unlock()
			if firstErr == nil {
				firstErr = err
			}
		}
		for range ctlApiClients {
			wg.Go(func() {
				api, err := controller.LoginAs(ctx, ctlenv.AdminUser, ctlenv.AdminPassword)
				if err != nil {
					keepFirst(err)
					return
				}
				defer api.Close()
				for time.Now().Before(deadline) {
					err = callApi(ctx, api)
					if err != nil {
						keepFirst(err)
						return
					}
				}
			})
		}

		// sampled while the clients still run, 10s before the end
		sampleAt := max(ctlApiFor-10*time.Second, ctlApiFor/2)
		select {
		case <-ctx.Done():
		case <-time.After(sampleAt):
		}
		addRow("api clients", target.now(ctx))
		wg.Wait()
		Expect(firstErr).NotTo(HaveOccurred())
	}, NodeTimeout(30*time.Minute))

	It("churns", func(ctx SpecContext) {
		before := target.settled(ctx)
		addRow("before churn", before)

		// the vips are all held by a subscriber, every change fans out through the announcers
		deadline := time.Now().Add(ctlChurnFor)
		for time.Now().Before(deadline) {
			backendUuid := createBackend(ctx, admin)
			vsResp, err := admin.VS.CreateVS(ctx, &svcvs.CreateVSRequest{Vs: ctlenv.NewTcpVs(ctlenv.Unique("vs"), 20000, backendUuid)})
			Expect(err).NotTo(HaveOccurred())
			vsUuid := vsResp.GetVs().GetUUID()
			vipUuid := vipUuids[mathrand.IntN(len(vipUuids))]
			changeVipVs(ctx, admin, vipUuid, func(uuids []string) []string { return append(uuids, vsUuid) })
			changeVipVs(ctx, admin, vipUuid, func(uuids []string) []string {
				return slices.DeleteFunc(uuids, func(uuid string) bool { return uuid == vsUuid })
			})
			_, err = admin.VS.DeleteVS(ctx, &svccore.UuidRequest{UUID: vsUuid})
			Expect(err).NotTo(HaveOccurred())
			_, err = admin.Backend.DeleteBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
			Expect(err).NotTo(HaveOccurred())
		}

		waitSynced(subscribers, vsType, wantVs)
		after := target.settled(ctx)
		addRow("after churn", after)
		table.Rows = append(table.Rows, measure.Delta("churn delta", before, after))
	}, NodeTimeout(30*time.Minute))
})

// controllerMetrics reads the controller's /metrics, its http routes need no token
func controllerMetrics(controller *ctlenv.Controller, certs *ctlenv.Certs) func(context.Context) (string, error) {
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: certs.UserTLSConfig(ctlenv.HostServerName)},
		Timeout:   30 * time.Second,
	}
	return func(ctx context.Context) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+controller.Addr(ctx)+"/metrics", nil)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("controller metrics: status %d: %s", resp.StatusCode, body)
		}
		return string(body), nil
	}
}

func createBackend(ctx context.Context, admin *ctlenv.Api) string {
	GinkgoHelper()
	resp, err := admin.Backend.CreateBackend(ctx, &svcbackend.CreateBackendRequest{Backend: ctlenv.NewBackend(ctlenv.Unique("backend"))})
	Expect(err).NotTo(HaveOccurred())
	return resp.GetBackend().GetUUID()
}

func changeVipVs(ctx context.Context, admin *ctlenv.Api, vipUuid string, change func([]string) []string) {
	GinkgoHelper()
	resp, err := admin.VIP.ReadVIP(ctx, &svccore.UuidRequest{UUID: vipUuid})
	Expect(err).NotTo(HaveOccurred())
	updated := resp.GetVip()
	updated.VirtualServersUuid = change(updated.GetVirtualServersUuid())
	_, err = admin.VIP.UpdateVIP(ctx, &svcvip.UpdateVIPRequest{UUID: vipUuid, Vip: updated})
	Expect(err).NotTo(HaveOccurred())
}

// callApi is one round of what a user of the api does: two lists and a create then delete
func callApi(ctx context.Context, api *ctlenv.Api) error {
	_, err := api.VS.ListVS(ctx, &svcvs.ListVSRequest{Limit: 100, Offset: int32(mathrand.IntN(ctlVs))})
	if err != nil {
		return fmt.Errorf("list virtual servers: %w", err)
	}
	_, err = api.Backend.ListBackend(ctx, &svcbackend.ListBackendRequest{Limit: 100, Offset: int32(mathrand.IntN(ctlBackends))})
	if err != nil {
		return fmt.Errorf("list backends: %w", err)
	}
	resp, err := api.Backend.CreateBackend(ctx, &svcbackend.CreateBackendRequest{Backend: ctlenv.NewBackend(ctlenv.Unique("api-backend"))})
	if err != nil {
		return fmt.Errorf("create backend: %w", err)
	}
	_, err = api.Backend.DeleteBackend(ctx, &svccore.UuidRequest{UUID: resp.GetBackend().GetUUID()})
	if err != nil {
		return fmt.Errorf("delete backend: %w", err)
	}
	return nil
}

// waitSynced waits until every subscriber holds the virtual servers of its instance
func waitSynced(subscribers map[string]*fakeagent.Subscriber, vsType string, wantVs map[string]int) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		for instanceUuid, subscriber := range subscribers {
			g.Expect(subscriber.Err()).NotTo(HaveOccurred())
			g.Expect(subscriber.State(vsType)).To(HaveLen(wantVs[instanceUuid]), "instance %s", instanceUuid)
		}
	}).Should(Succeed())
}

// selfSignedPem gives one certificate shared by every certificate resource, the store keeps a copy of each
func selfSignedPem() (certPem, keyPem string) {
	GinkgoHelper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "vhost.e2e.local"},
		DNSNames:     []string{"vhost.e2e.local"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	certDer, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	keyDer, err := x509.MarshalECPrivateKey(key)
	Expect(err).NotTo(HaveOccurred())
	certPem = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDer}))
	keyPem = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer}))
	return certPem, keyPem
}
