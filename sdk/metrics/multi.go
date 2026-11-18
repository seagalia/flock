package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// AgentSDKAppName holds the metrics of the agent sdk, which every agent mode enables next to its own
const AgentSDKAppName = "agentsdk"

// One binary holds every app, and their packages register their metrics at init whatever app the process runs. Each
// app's metrics wait in an ephemeral store until EnableAppMetrics moves them to the default registry, so /metrics only
// shows the apps the process runs. No lock: the apps enable their metrics one after the other before any of them runs,
// the embedded WAF enabling its own later is then alone to touch the maps.
var (
	pending = make(map[string]*EphemeralStoreRegisterer)
	enabled = make(map[string]bool)
)

// Registerer is the registerer of the app, for a library that registers its collectors itself
func Registerer(appName string) prometheus.Registerer {
	return registererFromAppName(appName)
}

func NewCounter(appName string, opts prometheus.CounterOpts) prometheus.Counter {
	return promauto.With(registererFromAppName(appName)).NewCounter(opts)
}

func NewCounterVec(appName string, opts prometheus.CounterOpts, labelNames []string) *prometheus.CounterVec {
	return promauto.With(registererFromAppName(appName)).NewCounterVec(opts, labelNames)
}

func NewCounterFunc(appName string, opts prometheus.CounterOpts, function func() float64) prometheus.CounterFunc {
	return promauto.With(registererFromAppName(appName)).NewCounterFunc(opts, function)
}

func NewGauge(appName string, opts prometheus.GaugeOpts) prometheus.Gauge {
	return promauto.With(registererFromAppName(appName)).NewGauge(opts)
}

func NewGaugeVec(appName string, opts prometheus.GaugeOpts, labelNames []string) *prometheus.GaugeVec {
	return promauto.With(registererFromAppName(appName)).NewGaugeVec(opts, labelNames)
}

func NewGaugeFunc(appName string, opts prometheus.GaugeOpts, function func() float64) prometheus.GaugeFunc {
	return promauto.With(registererFromAppName(appName)).NewGaugeFunc(opts, function)
}

func NewSummary(appName string, opts prometheus.SummaryOpts) prometheus.Summary {
	return promauto.With(registererFromAppName(appName)).NewSummary(opts)
}

func NewSummaryVec(appName string, opts prometheus.SummaryOpts, labelNames []string) *prometheus.SummaryVec {
	return promauto.With(registererFromAppName(appName)).NewSummaryVec(opts, labelNames)
}

func NewHistogram(appName string, opts prometheus.HistogramOpts) prometheus.Histogram {
	return promauto.With(registererFromAppName(appName)).NewHistogram(opts)
}

func NewHistogramVec(appName string, opts prometheus.HistogramOpts, labelNames []string) *prometheus.HistogramVec {
	return promauto.With(registererFromAppName(appName)).NewHistogramVec(opts, labelNames)
}

func NewUntypedFunc(appName string, opts prometheus.UntypedOpts, function func() float64) prometheus.UntypedFunc {
	return promauto.With(registererFromAppName(appName)).NewUntypedFunc(opts, function)
}

func MustRegister(appName string, cs ...prometheus.Collector) {
	registererFromAppName(appName).MustRegister(cs...)
}

func Register(appName string, cs prometheus.Collector) error {
	return registererFromAppName(appName).Register(cs)
}

func Unregister(appName string, cs prometheus.Collector) bool {
	return registererFromAppName(appName).Unregister(cs)
}

// registererFromAppName reads prometheus.DefaultRegisterer at each call, which specs replace with a registry of their own
func registererFromAppName(appName string) prometheus.Registerer {
	if enabled[appName] {
		return prometheus.DefaultRegisterer
	}
	store, ok := pending[appName]
	if !ok {
		store = NewEphemeralStoreRegisterer()
		pending[appName] = store
	}
	return store
}

// EnableAppMetrics moves the metrics of the app to the default registry, where it registers the next ones directly.
// Calling it again does nothing, flock dev runs several apps sharing the metrics of the agent sdk.
func EnableAppMetrics(appName string) {
	if enabled[appName] {
		return
	}
	enabled[appName] = true
	store, ok := pending[appName]
	if !ok {
		return
	}
	delete(pending, appName)
	prometheus.DefaultRegisterer.MustRegister(store.List()...)
}
