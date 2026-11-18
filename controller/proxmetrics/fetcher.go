package proxmetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"github.com/seagalia/flock/controller/dbstore"
	"github.com/seagalia/flock/controller/instantiaters"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

const instanceInfoPath = "instances-info"

type instanceInfo struct {
	UUID           string `json:"uuid"`
	Name           string `json:"name"`
	MetricEndpoint string `json:"metric_endpoint"`
}

type Fetcher struct {
	scraper      *Scraper
	instStorer   dbstore.LightStorer
	instantiater instantiaters.Instantiater
}

func NewFetcher(scraper *Scraper, instantiater instantiaters.Instantiater, instStorer dbstore.LightStorer) *Fetcher {
	return &Fetcher{
		scraper:      scraper,
		instStorer:   instStorer,
		instantiater: instantiater,
	}
}

func (f Fetcher) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	instanceUuid := req.PathValue("instance_uuid")
	err := req.ParseForm()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if instanceUuid == "" {
		instanceUuid = req.Form.Get("instance_uuid")
	}
	if instanceUuid == instanceInfoPath {
		f.serveInstancesInfo(w, req)
		return
	}
	promhttp.HandlerFor(
		prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
			return f.Metrics(req.Context(), instanceUuid, "/metrics")
		}),
		promhttp.HandlerOpts{}).ServeHTTP(w, req)
}

func (f Fetcher) serveInstancesInfo(w http.ResponseWriter, req *http.Request) {
	resMap, err := f.instStorer.LightList(req.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	u, err := url.Parse(req.URL.String())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	u.Path = "/metrics"
	u.RawFragment = ""
	u.RawQuery = ""
	finalUrl := u.String()
	instancesInfo := make([]*instanceInfo, 0)
	for uuid, name := range resMap {
		instancesInfo = append(instancesInfo, &instanceInfo{
			UUID:           uuid,
			Name:           name,
			MetricEndpoint: finalUrl + "/" + uuid,
		})
	}
	err = json.NewEncoder(w).Encode(instancesInfo)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (f Fetcher) Metrics(ctx context.Context, instanceUuid, path string) ([]*dto.MetricFamily, error) {
	agentsInfo, err := f.instantiater.ListAgents(ctx, instanceUuid)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, ErrNoInstanceFound(instanceUuid)
		}
	}
	finalAgents := make([]*agent.Agent, 0)
	for _, agentInfo := range agentsInfo {
		if agentInfo.GetFeatures().GetShowMetrics() {
			finalAgents = append(finalAgents, agentInfo)
		}
	}
	agentsInfo = finalAgents

	if len(agentsInfo) == 0 {
		return nil, fmt.Errorf("no agent with metrics feature available found for instance %s", instanceUuid)
	}

	jobs := make(chan *agent.Agent, len(agentsInfo))
	errFetch := &ErrFetch{}
	wg := &sync.WaitGroup{}

	muWrite := sync.Mutex{}
	metricsUnmerged := make([]*dto.MetricFamily, 0)
	healthyAgents := make([]*agent.Agent, 0)
	wg.Add(len(agentsInfo))
	for w := 1; w <= 5; w++ {
		go func(jobs <-chan *agent.Agent, errFetch *ErrFetch, path string) {
			for j := range jobs {
				newMetrics, err := f.Metric(ctx, j, path, instanceUuid)
				if err != nil {
					if errF, ok := err.(*ErrFetch); ok {
						muWrite.Lock()
						*errFetch = *errF
						muWrite.Unlock()
						wg.Done()
						continue
					}
					log.Warn().Msgf("Cannot get metric for instance %s in agent %s", instanceUuid, j.GetUUID())
					newMetrics = f.scrapeError(j, err)
				}
				muWrite.Lock()
				metricsUnmerged = append(metricsUnmerged, newMetrics...)
				muWrite.Unlock()

				if f.scraper.Ping(ctx, j) {
					muWrite.Lock()
					healthyAgents = append(healthyAgents, j)
					muWrite.Unlock()
				}
				wg.Done()
			}
		}(jobs, errFetch, path)
	}
	for _, agentInfo := range agentsInfo {
		jobs <- agentInfo
	}
	wg.Wait()
	close(jobs)

	// every agent of a cancelled scrape fails with the same context error, which would be exposed as one scrape error
	// metric per agent to a caller that already left
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if errFetch.Code != 0 {
		return make([]*dto.MetricFamily, 0), errFetch
	}

	metricsUnmerged = append(metricsUnmerged, f.statsAgent(healthyAgents, agentsInfo)...)
	return metricsUnmerged, nil
}

func (f Fetcher) Metric(
	ctx context.Context, agentInfo *agent.Agent, path string, instanceUuid string,
) ([]*dto.MetricFamily, error) {
	reader, err := f.scraper.Scrape(ctx, agentInfo, path, instanceUuid)
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
		for _, metric := range metricGroup.Metric {
			metric.Label = f.cleanMetricLabels(
				metric.Label,
				"instance",
				"agent_name",
				"agent_uuid",
				"agent_host",
				"agent_type",
			)
			metric.Label = append(metric.Label,
				&dto.LabelPair{
					Name:  new("instance"),
					Value: new(agentInfo.GetHost()),
				},
				&dto.LabelPair{
					Name:  new("agent_name"),
					Value: new(agentInfo.GetName()),
				},
				&dto.LabelPair{
					Name:  new("agent_uuid"),
					Value: new(agentInfo.GetUUID()),
				},
				&dto.LabelPair{
					Name:  new("agent_host"),
					Value: new(agentInfo.GetHost()),
				},
				&dto.LabelPair{
					Name:  new("agent_type"),
					Value: new(agentInfo.GetType()),
				},
			)

		}
	}
	finalMetrics := make([]*dto.MetricFamily, len(metricsGroup))
	i := 0
	for _, metricGroup := range metricsGroup {
		finalMetrics[i] = metricGroup
		i++
	}
	return finalMetrics, nil
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

func (f Fetcher) scrapeError(agentInfo *agent.Agent, err error) []*dto.MetricFamily {
	name := "controller_proxmetrics_scrape_error"
	help := "Controller proxy metrics scrap error on one agent"
	metric := prometheus.NewCounter(prometheus.CounterOpts{
		Name: name,
		Help: help,
		ConstLabels: prometheus.Labels{
			"instance":   agentInfo.GetHost(),
			"agent_name": agentInfo.GetName(),
			"agent_uuid": agentInfo.GetUUID(),
			"agent_host": agentInfo.GetHost(),
			"agent_type": agentInfo.GetType(),
			"error":      err.Error(),
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

func (f Fetcher) statsAgent(healthyAgents []*agent.Agent, allAgents []*agent.Agent) []*dto.MetricFamily {
	nameHy := "controller_proxmetrics_agents_healthy"
	helpHy := "Controller proxy agentMetrics to know if ag is healthy"
	nameTotal := "controller_proxmetrics_agents_total"
	helpTotal := "Controller proxy agentMetrics to know the total number of agents"
	metricType := dto.MetricType_GAUGE

	agentMetrics := make([]*dto.Metric, 0, len(allAgents))
	for _, agentInfo := range allAgents {
		healthy := float64(0)
		if lo.ContainsBy[*agent.Agent](healthyAgents, func(current *agent.Agent) bool {
			return current.GetUUID() == agentInfo.GetUUID()
		}) {
			healthy = 1
		}

		metric := prometheus.NewGauge(prometheus.GaugeOpts{
			Name: nameHy,
			Help: helpHy,
			ConstLabels: prometheus.Labels{
				"instance":   agentInfo.GetHost(),
				"agent_name": agentInfo.GetName(),
				"agent_uuid": agentInfo.GetUUID(),
				"agent_host": agentInfo.GetHost(),
				"agent_type": agentInfo.GetType(),
			},
		})
		metric.Set(healthy)
		var dtoMetric dto.Metric
		metric.Write(&dtoMetric) // nolint: errcheck
		agentMetrics = append(agentMetrics, &dtoMetric)
	}

	hyFamily := &dto.MetricFamily{
		Name:   new(nameHy),
		Help:   new(helpHy),
		Type:   &metricType,
		Metric: agentMetrics,
	}

	metric := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: nameTotal,
		Help: helpTotal,
	})

	metric.Set(float64(len(allAgents)))
	var dtoMetric dto.Metric
	metric.Write(&dtoMetric) // nolint: errcheck
	totalFamily := &dto.MetricFamily{
		Name:   new(nameTotal),
		Help:   new(helpTotal),
		Type:   &metricType,
		Metric: []*dto.Metric{&dtoMetric},
	}
	return []*dto.MetricFamily{hyFamily, totalFamily}
}
