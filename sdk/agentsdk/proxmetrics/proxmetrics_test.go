package proxmetrics_test

import (
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"

	. "github.com/seagalia/flock/sdk/agentsdk/proxmetrics"
	"github.com/seagalia/flock/sdk/config"
)

const exposition = `# HELP requests_total Requests
# TYPE requests_total counter
requests_total{code="200",target="overridden"} 3
`

func metricsServer(status int, body string, gzipped bool) (*httptest.Server, chan *http.Request) {
	requests := make(chan *http.Request, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests <- req
		if !gzipped {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(status)
		gz := gzip.NewWriter(w)
		_, _ = io.WriteString(gz, body)
		_ = gz.Close()
	}))
	DeferCleanup(server.Close)
	return server, requests
}

func targetOf(name string, server *httptest.Server) *ProxyMetricsTarget {
	u, err := url.Parse(server.URL + "/metrics")
	Expect(err).ToNot(HaveOccurred())
	return &ProxyMetricsTarget{Name: name, URL: u}
}

func labelValue(metric *dto.Metric, name string) string {
	for _, label := range metric.GetLabel() {
		if label.GetName() == name {
			return label.GetValue()
		}
	}
	return ""
}

func familyByName(mfs []*dto.MetricFamily, name string) *dto.MetricFamily {
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf
		}
	}
	return nil
}

var _ = Describe("Scraper", func() {
	var scraper *Scraper

	BeforeEach(func() {
		scraper = NewScraper(nil)
	})

	It("should scrape the target with auth, headers and scrape timeout", func() {
		server, requests := metricsServer(http.StatusOK, exposition, false)
		target := targetOf("app", server)
		target.User = "user"
		target.Password = "pass"
		target.Headers = map[string][]string{"X-Custom": {"value"}}
		target.ScrapeTimeout = config.Duration(5 * time.Second)

		reader, err := scraper.Scrape(target)
		Expect(err).ToNot(HaveOccurred())
		defer reader.Close()
		Expect(io.ReadAll(reader)).To(BeEquivalentTo(exposition))

		var req *http.Request
		Expect(requests).To(Receive(&req))
		user, pass, ok := req.BasicAuth()
		Expect(ok).To(BeTrue())
		Expect(user).To(Equal("user"))
		Expect(pass).To(Equal("pass"))
		Expect(req.Header.Get("X-Custom")).To(Equal("value"))
		Expect(req.Header.Get("X-Prometheus-Scrape-Timeout-Seconds")).To(Equal("5.000000"))
		Expect(req.Header.Get("X-Controller-Scrapping")).To(Equal("true"))
	})

	It("should decompress gzipped responses", func() {
		server, _ := metricsServer(http.StatusOK, exposition, true)
		reader, err := scraper.Scrape(targetOf("app", server))
		Expect(err).ToNot(HaveOccurred())
		defer reader.Close()
		Expect(io.ReadAll(reader)).To(BeEquivalentTo(exposition))
	})

	It("should give a fetch error when the endpoint is not found", func() {
		server, _ := metricsServer(http.StatusNotFound, "", false)
		_, err := scraper.Scrape(targetOf("app", server))
		var errFetch *ErrFetch
		Expect(err).To(BeAssignableToTypeOf(errFetch))
		Expect(err.(*ErrFetch).Code).To(Equal(http.StatusNotAcceptable))
		Expect(err.Error()).To(ContainSubstring("Target app (status code 404)"))
	})

	It("should scrape a target on a unix socket", func() {
		// macOS refuses unix socket paths over 104 bytes, and the one of GinkgoT().TempDir() is longer
		dir, err := os.MkdirTemp("", "scrape")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		socketPath := filepath.Join(dir, "metrics.sock")
		lis, err := net.Listen("unix", socketPath)
		Expect(err).ToNot(HaveOccurred())
		requests := make(chan *http.Request, 1)
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			requests <- req
			_, _ = io.WriteString(w, exposition)
		}), ReadHeaderTimeout: time.Second}
		go server.Serve(lis) //nolint:errcheck
		DeferCleanup(server.Close)
		targetURL, err := url.Parse("http://waf/metrics?filter=waf")
		Expect(err).ToNot(HaveOccurred())

		reader, err := scraper.Scrape(&ProxyMetricsTarget{Name: "waf", URL: targetURL, SocketPath: socketPath})
		Expect(err).ToNot(HaveOccurred())
		defer reader.Close()
		Expect(io.ReadAll(reader)).To(BeEquivalentTo(exposition))

		var req *http.Request
		Expect(requests).To(Receive(&req))
		Expect(req.URL.RequestURI()).To(Equal("/metrics?filter=waf"))
	})

	It("should scrape /metrics of a target on a unix socket with no url", func() {
		dir, err := os.MkdirTemp("", "scrape")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		socketPath := filepath.Join(dir, "metrics.sock")
		lis, err := net.Listen("unix", socketPath)
		Expect(err).ToNot(HaveOccurred())
		requests := make(chan *http.Request, 1)
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			requests <- req
			_, _ = io.WriteString(w, exposition)
		}), ReadHeaderTimeout: time.Second}
		go server.Serve(lis) //nolint:errcheck
		DeferCleanup(server.Close)

		reader, err := scraper.Scrape(&ProxyMetricsTarget{Name: "waf", SocketPath: socketPath})
		Expect(err).ToNot(HaveOccurred())
		defer reader.Close()
		Expect(io.ReadAll(reader)).To(BeEquivalentTo(exposition))

		var req *http.Request
		Expect(requests).To(Receive(&req))
		Expect(req.URL.RequestURI()).To(Equal("/metrics"))
	})

	It("should fail on server errors", func() {
		server, _ := metricsServer(http.StatusInternalServerError, "", false)
		_, err := scraper.Scrape(targetOf("app", server))
		Expect(err).To(MatchError(ContainSubstring("500")))
	})
})

var _ = Describe("Fetcher", func() {
	var scraper *Scraper

	BeforeEach(func() {
		scraper = NewScraper(nil)
	})

	It("should label target metrics with the target name", func() {
		server, _ := metricsServer(http.StatusOK, exposition, false)
		fetcher := NewFetcher(scraper, nil)

		mfs, err := fetcher.Metric(targetOf("app", server))
		Expect(err).ToNot(HaveOccurred())
		requests := familyByName(mfs, "requests_total")
		Expect(requests).ToNot(BeNil())
		Expect(labelValue(requests.GetMetric()[0], "target")).To(Equal("app"))
		Expect(labelValue(requests.GetMetric()[0], "code")).To(Equal("200"))
	})

	It("should merge metrics of every targets with the agent metrics", func() {
		server1, _ := metricsServer(http.StatusOK, exposition, false)
		server2, _ := metricsServer(http.StatusOK, exposition, true)
		fetcher := NewFetcher(scraper, []*ProxyMetricsTarget{targetOf("app1", server1), targetOf("app2", server2)})

		mfs, err := fetcher.Gather()
		Expect(err).ToNot(HaveOccurred())
		requests := familyByName(mfs, "requests_total")
		Expect(requests.GetMetric()).To(HaveLen(2))
		targets := []string{labelValue(requests.GetMetric()[0], "target"), labelValue(requests.GetMetric()[1], "target")}
		Expect(targets).To(ConsistOf("app1", "app2"))

		goroutines := familyByName(mfs, "go_goroutines")
		Expect(goroutines).ToNot(BeNil())
		Expect(labelValue(goroutines.GetMetric()[0], "target")).To(Equal("agent"))
	})

	It("should report a failing target as a scrape error metric", func() {
		server, _ := metricsServer(http.StatusInternalServerError, "", false)
		fetcher := NewFetcher(scraper, []*ProxyMetricsTarget{targetOf("broken", server)})

		mfs, err := fetcher.Gather()
		Expect(err).ToNot(HaveOccurred())
		scrapeErr := familyByName(mfs, "agent_proxmetrics_scrape_error")
		Expect(scrapeErr).ToNot(BeNil())
		Expect(labelValue(scrapeErr.GetMetric()[0], "target")).To(Equal("broken"))
	})

	It("should fail when a target endpoint is not found", func() {
		server, _ := metricsServer(http.StatusNotFound, "", false)
		fetcher := NewFetcher(scraper, []*ProxyMetricsTarget{targetOf("missing", server)})

		mfs, err := fetcher.Gather()
		Expect(err).To(HaveOccurred())
		Expect(mfs).To(BeEmpty())
	})

	It("should serve merged metrics over http", func() {
		server, _ := metricsServer(http.StatusOK, exposition, false)
		fetcher := NewFetcher(scraper, []*ProxyMetricsTarget{targetOf("app", server)})

		recorder := httptest.NewRecorder()
		fetcher.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Body.String()).To(ContainSubstring(`requests_total{code="200",target="app"} 3`))
	})

	It("should serve only the agent metrics when it has no target", func() {
		fetcher := NewFetcher(nil, nil)

		mfs, err := fetcher.Gather()
		Expect(err).ToNot(HaveOccurred())
		goroutines := familyByName(mfs, "go_goroutines")
		Expect(goroutines).ToNot(BeNil())
		Expect(labelValue(goroutines.GetMetric()[0], "target")).To(Equal("agent"))

		recorder := httptest.NewRecorder()
		fetcher.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics/agent", nil))
		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Body.String()).To(MatchRegexp(`go_goroutines\{target="agent"\} \d+`))
	})
})

var _ = Describe("Fetch errors", func() {
	It("should describe missing instance and endpoint", func() {
		Expect(ErrNoInstanceFound("instance-1").Code).To(Equal(http.StatusNotFound))
		Expect(ErrNoInstanceFound("instance-1").Error()).To(ContainSubstring("instance-1"))
		Expect(ErrNoEndpointFound("my%20app", "/metrics").Error()).To(ContainSubstring("'my app'"))
	})
})
