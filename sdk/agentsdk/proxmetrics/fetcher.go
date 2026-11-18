package proxmetrics

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/seagalia/flock/sdk/config"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

type Fetcher struct {
	scraper *Scraper
	targets []*ProxyMetricsTarget
}

type ProxyMetricsTarget struct {
	Name string
	URL  *url.URL
	// SocketPath, when set, is the unix socket the request goes to. URL then gives its path and query, /metrics when
	// URL is nil.
	SocketPath    string
	User          string
	Password      string
	Headers       map[string][]string
	ScrapeTimeout config.Duration
}

func NewFetcher(scraper *Scraper, targets []*ProxyMetricsTarget) *Fetcher {
	return &Fetcher{
		scraper: scraper,
		targets: targets,
	}
}

func (f Fetcher) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	promhttp.HandlerFor(f, promhttp.HandlerOpts{}).ServeHTTP(w, req)
}

func (f Fetcher) Gather() ([]*dto.MetricFamily, error) {
	jobs := make(chan *ProxyMetricsTarget, len(f.targets))
	errFetch := &ErrFetch{}
	wg := &sync.WaitGroup{}

	muWrite := sync.Mutex{}
	metricsUnmerged := make([]*dto.MetricFamily, 0)

	wg.Add(len(f.targets))
	for w := 1; w <= 5; w++ {
		go func(jobs <-chan *ProxyMetricsTarget, errFetch *ErrFetch) {
			for j := range jobs {
				newMetrics, err := f.Metric(j)
				if err != nil {
					if errF, ok := err.(*ErrFetch); ok {
						muWrite.Lock()
						*errFetch = *errF
						muWrite.Unlock()
						wg.Done()
						continue
					}
					log.Warn().Msgf("Cannot get metric for target %s", j.Name)
					newMetrics = f.scrapeError(j, err)
				}
				muWrite.Lock()
				metricsUnmerged = append(metricsUnmerged, newMetrics...)
				muWrite.Unlock()
				wg.Done()
			}
		}(jobs, errFetch)
	}
	for _, target := range f.targets {
		jobs <- target
	}
	wg.Wait()
	close(jobs)
	if errFetch.Code != 0 {
		return make([]*dto.MetricFamily, 0), errFetch
	}
	gat := prometheus.ToTransactionalGatherer(prometheus.DefaultGatherer)
	mfs, done, err := gat.Gather()
	defer done()
	if err != nil {
		return nil, fmt.Errorf("cannot gather prometheus metrics: %w", err)
	}
	for _, mf := range mfs {
		f.addTargetName(mf, "agent")
	}
	metricsUnmerged = append(metricsUnmerged, mfs...)

	if len(metricsUnmerged) == 0 {
		return make([]*dto.MetricFamily, 0), nil
	}

	return f.cleanDuplicate(metricsUnmerged), nil
}

func (f Fetcher) cleanDuplicate(mfs []*dto.MetricFamily) []*dto.MetricFamily {
	mfMap := make(map[string]*dto.MetricFamily)
	for _, mf := range mfs {
		if _, ok := mfMap[mf.GetName()]; !ok {
			mfMap[mf.GetName()] = &dto.MetricFamily{
				Name:   mf.Name,
				Help:   mf.Help,
				Type:   mf.Type,
				Metric: mf.Metric,
			}
			continue
		}
		elem := mfMap[mf.GetName()]
		if elem.Help == nil {
			elem.Help = mf.Help
		}
		if elem.Type == nil {
			elem.Type = mf.Type
		}
		elem.Metric = append(mfMap[mf.GetName()].Metric, mf.Metric...)
	}
	mfs = make([]*dto.MetricFamily, 0, len(mfMap))
	for _, mf := range mfMap {
		mfs = append(mfs, mf)
	}
	return mfs
}

func (f Fetcher) Metric(target *ProxyMetricsTarget) ([]*dto.MetricFamily, error) {
	reader, err := f.scraper.Scrape(target)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	// legacy validation keeps the metric name rules prometheus applied before the utf-8 names
	parser := expfmt.NewTextParser(model.LegacyValidation)
	metricsGroup, err := parser.TextToMetricFamilies(reader)
	if err != nil {
		return nil, err
	}

	for _, metricGroup := range metricsGroup {
		f.addTargetName(metricGroup, target.Name)
	}
	finalMetrics := make([]*dto.MetricFamily, len(metricsGroup))
	i := 0
	for _, metricGroup := range metricsGroup {
		finalMetrics[i] = metricGroup
		i++
	}
	return finalMetrics, nil
}

func (f Fetcher) addTargetName(mf *dto.MetricFamily, targetName string) {
	for _, metric := range mf.Metric {
		metric.Label = f.cleanMetricLabels(
			metric.Label,
			"target",
		)
		metric.Label = append(metric.Label, &dto.LabelPair{
			Name:  new("target"),
			Value: new(targetName),
		})
	}
}

func (f Fetcher) cleanMetricLabels(labels []*dto.LabelPair, names ...string) []*dto.LabelPair {
	finalLabels := make([]*dto.LabelPair, 0)
	for _, label := range labels {
		toAdd := true
		for _, name := range names {
			if label.Name != nil && *label.Name == name {
				toAdd = false
				break
			}
		}
		if toAdd {
			finalLabels = append(finalLabels, label)
		}
	}
	return finalLabels
}

func (f Fetcher) scrapeError(target *ProxyMetricsTarget, err error) []*dto.MetricFamily {
	name := "agent_proxmetrics_scrape_error"
	help := "Agent proxy metrics scrap error on one agent"
	metric := prometheus.NewCounter(prometheus.CounterOpts{
		Name: name,
		Help: help,
		ConstLabels: prometheus.Labels{
			"target": target.Name,
			"error":  err.Error(),
		},
	})
	metric.Inc()
	var dtoMetric dto.Metric
	metric.Write(&dtoMetric) // nolint: errcheck
	metricType := dto.MetricType_COUNTER
	return []*dto.MetricFamily{
		{
			Name:   new(name),
			Help:   new(help),
			Type:   &metricType,
			Metric: []*dto.Metric{&dtoMetric},
		},
	}
}
