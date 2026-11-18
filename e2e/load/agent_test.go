//go:build load

package load_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/flock/extensions/announces/starling_announce/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/seagalia/flock/agent/starling/config"
	agentenv "github.com/seagalia/flock/e2e/agent/starling/env"
	"github.com/seagalia/flock/e2e/agent/starling/res"
	"github.com/seagalia/flock/e2e/images"
	"github.com/seagalia/flock/e2e/load/measure"
)

// the agent side runs on its own subnet, so it never collides with the networks of the other suites
const (
	agentPrefix   = "172.31.252"
	agentIP       = agentPrefix + ".100"
	agentHttpbin1 = agentPrefix + ".11"
	agentHttpbin2 = agentPrefix + ".12"
	agentClientIP = agentPrefix + ".50"
	agentInstance = "load"
	routedVips    = "10.252.0.0/16"
	// vsPerVip virtual servers share a vip, on ports 10000 and up
	vsPerVip       = 100
	trafficBackend = "b-traffic"
	// trafficVsCount virtual servers take the connections of one connections phase
	trafficVsCount = 10
	connholdPath   = "/usr/local/bin/connhold"
	portservePath  = "/usr/local/bin/portserve"
	// healthyPortsFrom is the first port portserve answers on in the client, one port per healthy endpoint
	healthyPortsFrom = 20000
	// endpointBackend is the uuid format of the backends of the endpoints phases
	endpointBackend = "b-ep-%d"
	// requestEvery stays under the 5s idle timeout of go-httpbin, which would close the connections
	requestEvery = 2 * time.Second
	agentMetrics = "https://127.0.0.1:18000/metrics/agent"
)

// connholdStatus is the status file connhold writes
type connholdStatus struct {
	Established int    `json:"established"`
	Failed      int    `json:"failed"`
	Requests    int    `json:"requests"`
	Done        bool   `json:"done"`
	LastError   string `json:"last_error"`
}

var (
	l3logPacketsMetric    = regexp.MustCompile(`flock_agent_l3logs_incoming_packets_total\{[^}]*\} ([0-9.e+]+)`)
	healthyEndpointMetric = regexp.MustCompile(`(?m)^flock_agent_backendctl_endpoint_state\{backend_uuid="b-ep-[^}]*\} 1$`)
)

var _ = Describe("agent", Ordered, Label("agent"), func() {
	vsType := helpers.TypeUrl[*vs.VirtualServer]()
	vipType := helpers.TypeUrl[*vip.VIP]()
	table := &measure.Table{Title: "agent", ContainerNote: "container = agent process + envoy"}
	var (
		e2eEnv *agentenv.Env
		agent  *agentenv.Agent
		client *agentenv.Client
		target measured
		// largeVs holds the current version of every virtual server of the large config, by vip index
		largeVs   map[int][]*vs.VirtualServer
		largeVips []*vip.VIP
	)

	addRow := func(phase string, sample measure.Sample) {
		table.Rows = append(table.Rows, measure.Row{Phase: phase, Sample: sample})
	}

	push := func(ctx context.Context, resources ...proto.Message) {
		GinkgoHelper()
		e2eEnv.Controller.Push(agentInstance, resources...)
		waitAcked(ctx, e2eEnv)
	}

	BeforeAll(func(ctx SpecContext) {
		DeferCleanup(func() { report.Tables = append(report.Tables, *table) })
		var err error
		e2eEnv, err = agentenv.SetupOn(ctx, agentPrefix)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) { e2eEnv.Teardown(ctx) }, NodeTimeout(2*time.Minute))

		_, err = e2eEnv.StartHttpbin(ctx, "httpbin-1", agentHttpbin1)
		Expect(err).NotTo(HaveOccurred())
		_, err = e2eEnv.StartHttpbin(ctx, "httpbin-2", agentHttpbin2)
		Expect(err).NotTo(HaveOccurred())
		client, err = e2eEnv.StartClient(ctx, agentClientIP)
		Expect(err).NotTo(HaveOccurred())
		connhold := filepath.Join(repoRoot, "e2e", ".build", "connhold")
		Expect(images.BuildLinuxBinary(ctx, repoRoot, "./e2e/load/connhold", connhold)).To(Succeed())
		Expect(client.Container.CopyFileToContainer(ctx, connhold, connholdPath, 0o755)).To(Succeed())
		portserve := filepath.Join(repoRoot, "e2e", ".build", "portserve")
		Expect(images.BuildLinuxBinary(ctx, repoRoot, "./e2e/load/portserve", portserve)).To(Succeed())
		Expect(client.Container.CopyFileToContainer(ctx, portserve, portservePath, 0o755)).To(Succeed())

		agent, err = e2eEnv.StartAgent(ctx, agentenv.AgentOptions{
			Name: "agent-load", IP: agentIP, Instance: agentInstance,
			// debug lines would allocate in the measured process, production runs at info
			Configure: func(conf *config.Config) { conf.Log.Level = "info" },
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(client.AddRoute(ctx, routedVips, agentIP)).To(Succeed())
		// without config, envoy is live only once its initial xds fetches time out
		Eventually(func() (string, error) {
			return agent.Exec(ctx, "curl", "-s", "http://127.0.0.1:19000/ready")
		}).Should(ContainSubstring("LIVE"))

		target = measured{
			metrics:   func(ctx context.Context) (string, error) { return agent.Exec(ctx, "curl", "-sk", agentMetrics) },
			container: agent.Container,
		}
		addRow("idle", target.settled(ctx))
	}, NodeTimeout(20*time.Minute))

	It("holds a large config", func(ctx SpecContext) {
		largeVs = make(map[int][]*vs.VirtualServer)
		resources := []proto.Message{res.Backend(trafficBackend, []string{agentHttpbin1 + ":8080", agentHttpbin2 + ":8080"})}
		for k := range agentVs {
			uuid := fmt.Sprintf("vs-%d", k)
			port := uint32(10000 + k%vsPerVip)
			virtualServer := res.TcpVs(uuid, port, trafficBackend)
			isHttp := k%2 == 1
			if isHttp {
				vhost := res.HttpRoute(res.VirtualHost([]string{uuid + ".load"}), trafficBackend)
				virtualServer = res.HttpVs(uuid, port, vhost)
			}
			largeVs[k/vsPerVip] = append(largeVs[k/vsPerVip], virtualServer)
			resources = append(resources, virtualServer)
		}
		largeVips = make([]*vip.VIP, len(largeVs))
		for v := range largeVips {
			largeVips[v] = res.Vip(fmt.Sprintf("vip-%d", v), vipIP(v), starling_announce.StarlingAnnounce_STATIC, uuidsOf(largeVs[v])...)
			resources = append(resources, largeVips[v])
		}
		push(ctx, resources...)
		addRow("large config", target.settled(ctx))
	}, NodeTimeout(30*time.Minute))

	It("health checks many endpoints", func(ctx SpecContext) {
		// endpoints are closed ports of the httpbins: distinct ip:port, health checks fail fast with a reset
		endpointCount := agentBackends * agentEndpoints
		Expect(20000+endpointCount/2).To(BeNumerically("<", 65536), "too many endpoints for the ports of two httpbins")
		every5s := func(b *backend.Backend) { b.HealthCheck.Interval = durationpb.New(5 * time.Second) }
		resources := make([]proto.Message, 0)
		backendUuids := make([]string, agentBackends)
		for i := range agentBackends {
			endpoints := make([]string, agentEndpoints)
			for j := range agentEndpoints {
				e := i*agentEndpoints + j
				ip := []string{agentHttpbin1, agentHttpbin2}[e%2]
				endpoints[j] = net.JoinHostPort(ip, strconv.Itoa(20000+e/2))
			}
			backendUuids[i] = fmt.Sprintf(endpointBackend, i)
			resources = append(resources, res.Backend(backendUuids[i], endpoints, every5s))
		}

		// backends are only resolved and health checked once a virtual server uses them
		switched := 0
		for v := 0; v < len(largeVs) && switched < agentBackends; v++ {
			for n, virtualServer := range largeVs[v] {
				if switched == agentBackends {
					break
				}
				isHttp := virtualServer.GetTcpRouteConfig() == nil
				if isHttp {
					continue
				}
				updated := proto.CloneOf(virtualServer)
				updated.GetTcpRouteConfig().BackendUuid = backendUuids[switched]
				largeVs[v][n] = updated
				resources = append(resources, updated)
				switched++
			}
		}
		Expect(switched).To(Equal(agentBackends), "only %d tcp virtual servers to use the backends", switched)
		push(ctx, resources...)
		addRow("many endpoints (tcp, failing)", target.settled(ctx))
	}, NodeTimeout(30*time.Minute))

	It("health checks many healthy http endpoints", func(ctx SpecContext) {
		// a succeeding http check keeps a connection open to its endpoint, a failing one does not
		endpointCount := agentBackends * agentEndpoints
		lastPort := healthyPortsFrom + endpointCount - 1
		Expect(lastPort).To(BeNumerically("<", 32768), "too many endpoints for the ports below the ephemeral range")
		_, err := client.Exec(ctx, "sh", "-c", fmt.Sprintf("%s -from %d -count %d > /tmp/portserve.log 2>&1 &",
			portservePath, healthyPortsFrom, endpointCount))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() (string, error) {
			return client.Exec(ctx, "curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", fmt.Sprintf("http://127.0.0.1:%d/", lastPort))
		}).WithTimeout(time.Minute).Should(Equal("200"))

		httpEvery5s := func(b *backend.Backend) {
			b.HealthCheck.Interval = durationpb.New(5 * time.Second)
			b.HealthCheck.HealthChecker = &core.HealthCheck_HttpHealthCheck{HttpHealthCheck: &core.HttpHealthCheck{Path: "/"}}
		}
		// same uuids as the failing endpoints: the virtual servers already use these backends
		resources := make([]proto.Message, agentBackends)
		for i := range agentBackends {
			endpoints := make([]string, agentEndpoints)
			for j := range agentEndpoints {
				endpoints[j] = net.JoinHostPort(agentClientIP, strconv.Itoa(healthyPortsFrom+i*agentEndpoints+j))
			}
			resources[i] = res.Backend(fmt.Sprintf(endpointBackend, i), endpoints, httpEvery5s)
		}
		push(ctx, resources...)
		Eventually(func() (int, error) {
			metrics, err := target.metrics(ctx)
			return len(healthyEndpointMetric.FindAllString(metrics, -1)), err
		}).WithTimeout(10*time.Minute).WithPolling(5*time.Second).Should(Equal(endpointCount), "endpoints healthy")
		addRow("many endpoints (http, healthy)", target.settled(ctx))
	}, NodeTimeout(30*time.Minute))

	holdConnections := func(ctx context.Context, vipUuid, ip string, opts ...res.VsOpt) {
		GinkgoHelper()
		resources := make([]proto.Message, 0, trafficVsCount+1)
		vsUuids := make([]string, trafficVsCount)
		addrs := make([]string, trafficVsCount)
		for n := range trafficVsCount {
			port := 9000 + n
			vsUuids[n] = fmt.Sprintf("%s-vs-%d", vipUuid, n)
			addrs[n] = net.JoinHostPort(ip, strconv.Itoa(port))
			resources = append(resources, res.TcpVs(vsUuids[n], uint32(port), trafficBackend, opts...))
		}
		resources = append(resources, res.Vip(vipUuid, ip, starling_announce.StarlingAnnounce_STATIC, vsUuids...))
		push(ctx, resources...)

		statusPath := "/tmp/" + vipUuid + ".json"
		finished := make(chan error, 1)
		go func() {
			_, err := client.Exec(ctx, connholdPath,
				"-addrs", strings.Join(addrs, ","), "-conns", strconv.Itoa(agentConns),
				"-hold", agentHoldFor.String(), "-every", requestEvery.String(), "-status", statusPath)
			finished <- err
		}()
		readStatus := func() (connholdStatus, error) {
			out, err := client.Exec(ctx, "cat", statusPath)
			if err != nil {
				return connholdStatus{}, err
			}
			var status connholdStatus
			return status, json.Unmarshal([]byte(out), &status)
		}
		Eventually(func(g Gomega) {
			status, err := readStatus()
			g.Expect(err).NotTo(HaveOccurred())
			// done before all were established means the hold ended without them
			cannotHold := status.Failed > 0 || status.Done
			if cannotHold {
				StopTrying(fmt.Sprintf("connhold could not hold the connections: %+v", status)).Now()
			}
			g.Expect(status.Established).To(Equal(agentConns))
		}).WithTimeout(agentHoldFor).Should(Succeed())

		addRow(fmt.Sprintf("connections (%s)", strings.TrimPrefix(vipUuid, "vip-")), target.now(ctx))

		Eventually(finished).WithTimeout(agentHoldFor + time.Minute).Should(Receive(BeNil()))
		status, err := readStatus()
		Expect(err).NotTo(HaveOccurred())
		Expect(status).To(And(HaveField("Done", true), HaveField("Failed", 0), HaveField("Established", agentConns)),
			"connhold status %+v", status)
	}

	It("holds plain connections", func(ctx SpecContext) {
		holdConnections(ctx, "vip-plain", "10.252.255.1")
	}, NodeTimeout(30*time.Minute))

	It("holds connections with l3 logs", func(ctx SpecContext) {
		holdConnections(ctx, "vip-l3log", "10.252.255.2", res.WithL3Log())
		metrics, err := target.metrics(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(sumOf(l3logPacketsMetric, metrics)).To(BeNumerically(">", 0), "the l3 log path saw no packet")
	}, NodeTimeout(30*time.Minute))

	It("churns", func(ctx SpecContext) {
		before := target.settled(ctx)
		addRow("before churn", before)

		// vip 0 and its virtual servers go away and come back, then their connection limit flips
		churnedVip := largeVips[0]
		churnedVs := largeVs[0]
		limitedVs := make([]proto.Message, len(churnedVs))
		originalVs := make([]proto.Message, len(churnedVs))
		for n, virtualServer := range churnedVs {
			limitedVs[n] = res.Apply(proto.CloneOf(virtualServer), res.WithConnLimit(1000))
			originalVs[n] = virtualServer
		}
		deadline := time.Now().Add(agentChurnFor)
		for time.Now().Before(deadline) {
			e2eEnv.Controller.Remove(agentInstance, vipType, churnedVip.GetUUID())
			for _, virtualServer := range churnedVs {
				e2eEnv.Controller.Remove(agentInstance, vsType, virtualServer.GetUUID())
			}
			waitAcked(ctx, e2eEnv)
			push(ctx, append(slices.Clone(originalVs), churnedVip)...)
			push(ctx, limitedVs...)
			push(ctx, originalVs...)
		}

		after := target.settled(ctx)
		addRow("after churn", after)
		table.Rows = append(table.Rows, measure.Delta("churn delta", before, after))
	}, NodeTimeout(30*time.Minute))
})

func waitAcked(ctx context.Context, e2eEnv *agentenv.Env) {
	GinkgoHelper()
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	Expect(e2eEnv.Controller.WaitAcked(waitCtx, agentInstance)).To(Succeed())
}

func vipIP(v int) string {
	return fmt.Sprintf("10.252.%d.%d", v/250, v%250+1)
}

func uuidsOf(virtualServers []*vs.VirtualServer) []string {
	uuids := make([]string, len(virtualServers))
	for n, virtualServer := range virtualServers {
		uuids[n] = virtualServer.GetUUID()
	}
	return uuids
}

func sumOf(metric *regexp.Regexp, metrics string) float64 {
	total := 0.0
	for _, match := range metric.FindAllStringSubmatch(metrics, -1) {
		value, err := strconv.ParseFloat(match[1], 64)
		if err == nil {
			total += value
		}
	}
	return total
}
