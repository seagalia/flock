package proxmetrics_test

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	"github.com/seagalia/flock/controller/instantiaters"
	. "github.com/seagalia/flock/controller/proxmetrics"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
)

const agentMetrics = `# HELP requests_total Total requests.
# TYPE requests_total counter
requests_total{instance="to-override",code="200"} 42
`

type fakeAgent struct {
	server *httptest.Server
	status int
	gzip   bool
	// hang holds every request until the client gives up
	hang bool
	// mutex protects lastHeader as fetcher scrapes agents concurrently
	mutex      sync.Mutex
	lastHeader http.Header
}

func newFakeAgent() *fakeAgent {
	fake := &fakeAgent{status: http.StatusOK}
	fake.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fake.mutex.Lock()
		fake.lastHeader = req.Header.Clone()
		hang := fake.hang
		fake.mutex.Unlock()
		if hang {
			<-req.Context().Done()
			return
		}
		if fake.status != http.StatusOK {
			w.WriteHeader(fake.status)
			return
		}
		if !fake.gzip {
			io.WriteString(w, agentMetrics) // nolint:errcheck
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		io.WriteString(gz, agentMetrics) // nolint:errcheck
		gz.Close()
	}))
	return fake
}

func (f *fakeAgent) agent(uuid string) *agent.Agent {
	return &agent.Agent{
		UUID:     uuid,
		Name:     "name-" + uuid,
		Host:     f.server.Listener.Addr().String(),
		Type:     "starling",
		Features: &agent.AgentFeatures{ShowMetrics: true},
	}
}

type fakeInstantiater struct {
	instantiaters.Null
	agents []*agent.Agent
	err    error
}

func (f *fakeInstantiater) ListAgents(_ context.Context, instanceUuid string) ([]*agent.Agent, error) {
	return f.agents, f.err
}

type fakeLightStorer map[string]string

func (f fakeLightStorer) LightList(_ context.Context) (map[string]string, error) {
	return f, nil
}

func labelValue(metric *dto.Metric, name string) string {
	for _, label := range metric.GetLabel() {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

func findFamily(families []*dto.MetricFamily, name string) *dto.MetricFamily {
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	return nil
}

var _ = Describe("Errors", func() {
	It("should give not found error for instance", func() {
		err := ErrNoInstanceFound("inst-1")
		Expect(err.Code).To(Equal(http.StatusNotFound))
		Expect(err.Error()).To(ContainSubstring("Cannot found instance inst-1"))
	})
	It("should give not acceptable error with unescaped app path", func() {
		err := ErrNoEndpointFound("my%20app", "/metrics")
		Expect(err.Code).To(Equal(http.StatusNotAcceptable))
		Expect(err.Error()).To(ContainSubstring("endpoint '/metrics' for app with id or path 'my app'"))
	})
})

var _ = Describe("Scraper", func() {
	var fake *fakeAgent
	var scraper *Scraper
	BeforeEach(func() {
		fake = newFakeAgent()
		DeferCleanup(fake.server.Close)
		scraper = NewScraper(&tls.Config{InsecureSkipVerify: true}) // nolint:gosec
	})

	It("should scrape agent with controller headers", func() {
		reader, err := scraper.Scrape(context.Background(), fake.agent("a1"), "/metrics", "inst-1")
		Expect(err).ToNot(HaveOccurred())
		defer reader.Close()

		content, err := io.ReadAll(reader)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(content)).To(Equal(agentMetrics))
		fake.mutex.Lock()
		defer fake.mutex.Unlock()
		Expect(fake.lastHeader.Get("X-Controller-Scrapping")).To(Equal("true"))
		Expect(fake.lastHeader.Get("X-Ctx-Instance-Uuid")).To(Equal("inst-1"))
	})
	It("should decompress gzip content", func() {
		fake.gzip = true

		reader, err := scraper.Scrape(context.Background(), fake.agent("a1"), "/metrics", "inst-1")
		Expect(err).ToNot(HaveOccurred())
		defer reader.Close()

		content, err := io.ReadAll(reader)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(content)).To(Equal(agentMetrics))
	})
	It("should give endpoint not found error on client error", func() {
		fake.status = http.StatusNotFound

		_, err := scraper.Scrape(context.Background(), fake.agent("a1"), "/metrics", "inst-1")

		Expect(err).To(BeAssignableToTypeOf(&ErrFetch{}))
		Expect(err.(*ErrFetch).Code).To(Equal(http.StatusNotAcceptable))
	})
	It("should give generic error on server error", func() {
		fake.status = http.StatusInternalServerError

		_, err := scraper.Scrape(context.Background(), fake.agent("a1"), "/metrics", "inst-1")

		Expect(err).To(MatchError(ContainSubstring("server returned HTTP status 500")))
	})
	It("should stop the fetch when the context is cancelled", func() {
		fake.mutex.Lock()
		fake.hang = true
		fake.mutex.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := scraper.Scrape(ctx, fake.agent("a1"), "/metrics", "inst-1")

		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
	})
	It("should ping reachable agent", func() {
		Expect(scraper.Ping(context.Background(), fake.agent("a1"))).To(BeTrue())
		Expect(scraper.Ping(context.Background(), &agent.Agent{Host: "127.0.0.1:1"})).To(BeFalse())
	})
	It("should refuse invalid gzip content", func() {
		_, err := NewReaderGzip(io.NopCloser(strings.NewReader("not gzip")))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("Fetcher", func() {
	var fake *fakeAgent
	var inst *fakeInstantiater
	var fetcher *Fetcher
	BeforeEach(func() {
		fake = newFakeAgent()
		DeferCleanup(fake.server.Close)
		inst = &fakeInstantiater{}
		scraper := NewScraper(&tls.Config{InsecureSkipVerify: true}) // nolint:gosec
		fetcher = NewFetcher(scraper, inst, fakeLightStorer{"inst-1": "my-instance"})
	})

	Context("Metrics", func() {
		It("should merge metrics of agents with agent labels and health stats", func() {
			inst.agents = []*agent.Agent{fake.agent("a1"), fake.agent("a2")}

			families, err := fetcher.Metrics(context.Background(), "inst-1", "/metrics")
			Expect(err).ToNot(HaveOccurred())

			requests := families[0:2]
			for _, family := range requests {
				Expect(family.GetName()).To(Equal("requests_total"))
				metric := family.GetMetric()[0]
				Expect(labelValue(metric, "instance")).To(Equal(fake.agent("a1").GetHost()))
				Expect(labelValue(metric, "code")).To(Equal("200"))
				Expect(labelValue(metric, "agent_type")).To(Equal("starling"))
			}
			healthy := findFamily(families, "controller_proxmetrics_agents_healthy")
			Expect(healthy.GetMetric()).To(HaveLen(2))
			Expect(healthy.GetMetric()[0].GetGauge().GetValue()).To(Equal(float64(1)))
			total := findFamily(families, "controller_proxmetrics_agents_total")
			Expect(total.GetMetric()[0].GetGauge().GetValue()).To(Equal(float64(2)))
		})
		It("should ignore agents without metrics feature", func() {
			withoutMetrics := fake.agent("a2")
			withoutMetrics.Features = nil
			inst.agents = []*agent.Agent{fake.agent("a1"), withoutMetrics}

			families, err := fetcher.Metrics(context.Background(), "inst-1", "/metrics")
			Expect(err).ToNot(HaveOccurred())

			total := findFamily(families, "controller_proxmetrics_agents_total")
			Expect(total.GetMetric()[0].GetGauge().GetValue()).To(Equal(float64(1)))
		})
		It("should give scrape error metric when agent fails", func() {
			fake.status = http.StatusInternalServerError
			inst.agents = []*agent.Agent{fake.agent("a1")}

			families, err := fetcher.Metrics(context.Background(), "inst-1", "/metrics")
			Expect(err).ToNot(HaveOccurred())

			scrapeErr := findFamily(families, "controller_proxmetrics_scrape_error")
			Expect(scrapeErr).ToNot(BeNil())
			Expect(labelValue(scrapeErr.GetMetric()[0], "agent_uuid")).To(Equal("a1"))
		})
		It("should fail when agent has no metrics endpoint", func() {
			fake.status = http.StatusNotFound
			inst.agents = []*agent.Agent{fake.agent("a1")}

			_, err := fetcher.Metrics(context.Background(), "inst-1", "/metrics")

			Expect(err).To(BeAssignableToTypeOf(&ErrFetch{}))
		})
		It("should stop the agent fetches when the context is cancelled", func() {
			fake.mutex.Lock()
			fake.hang = true
			fake.mutex.Unlock()
			inst.agents = []*agent.Agent{fake.agent("a1")}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			start := time.Now()
			_, err := fetcher.Metrics(ctx, "inst-1", "/metrics")

			Expect(err).To(MatchError(context.DeadlineExceeded))
			Expect(time.Since(start)).To(BeNumerically("<", 3*time.Second))
		})
		It("should fail when instance is not found", func() {
			inst.err = fmt.Errorf("service instance inst-1 not found")

			_, err := fetcher.Metrics(context.Background(), "inst-1", "/metrics")

			Expect(err).To(Equal(ErrNoInstanceFound("inst-1")))
		})
		It("should fail when no agent is available", func() {
			_, err := fetcher.Metrics(context.Background(), "inst-1", "/metrics")
			Expect(err).To(MatchError(ContainSubstring("no agent with metrics feature available found for instance inst-1")))
		})
	})

	Context("ServeHTTP", func() {
		It("should serve metrics of instance given in path", func() {
			inst.agents = []*agent.Agent{fake.agent("a1")}
			req := httptest.NewRequest(http.MethodGet, "/metrics/inst-1", nil)
			req.SetPathValue("instance_uuid", "inst-1")
			recorder := httptest.NewRecorder()

			fetcher.ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("requests_total"))
			Expect(recorder.Body.String()).To(ContainSubstring(`agent_uuid="a1"`))
		})
		It("should serve metrics of instance given in query", func() {
			inst.agents = []*agent.Agent{fake.agent("a1")}
			req := httptest.NewRequest(http.MethodGet, "/metrics?instance_uuid=inst-1", nil)
			recorder := httptest.NewRecorder()

			fetcher.ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(ContainSubstring("requests_total"))
		})
		It("should serve instances info", func() {
			req := httptest.NewRequest(http.MethodGet, "https://controller.local/metrics/instances-info?foo=bar", nil)
			req.SetPathValue("instance_uuid", "instances-info")
			recorder := httptest.NewRecorder()

			fetcher.ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			var infos []map[string]string
			Expect(json.Unmarshal(recorder.Body.Bytes(), &infos)).To(Succeed())
			Expect(infos).To(ConsistOf(map[string]string{
				"uuid":            "inst-1",
				"name":            "my-instance",
				"metric_endpoint": "https://controller.local/metrics/inst-1",
			}))
		})
	})
})
