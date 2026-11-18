package stores

import (
	"context"
	sdkmetrics "github.com/seagalia/flock/sdk/metrics"

	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
)

const (
	countTimeout = 30 * time.Second
)

// counted is the store the number_resources metrics read, set by the command running the app. Every /metrics of a
// process reads the same registry, so flock dev, which runs the starling agent and the provisioner together, counts the
// agent's store only, whatever order the apps are built in.
var (
	countedMutex sync.RWMutex
	counted      storers.StoreManager
)

func init() {
	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "stores",
		Name:      "number_resources",
		ConstLabels: map[string]string{
			"resource": helpers.TypeUrl[*backend.Backend](),
		},
		Help: "Number of resources stored",
	}, func() float64 {
		return float64(LenStore[*backend.Backend]())
	}))

	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "stores",
		Name:      "number_resources",
		ConstLabels: map[string]string{
			"resource": helpers.TypeUrl[*vip.VIP](),
		},
		Help: "Number of resources stored",
	}, func() float64 {
		return float64(LenStore[*vip.VIP]())
	}))

	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "stores",
		Name:      "number_resources",
		ConstLabels: map[string]string{
			"resource": helpers.TypeUrl[*vs.VirtualServer](),
		},
		Help: "Number of resources stored",
	}, func() float64 {
		return float64(LenStore[*vs.VirtualServer]())
	}))

	sdkmetrics.MustRegister(sdkmetrics.AgentSDKAppName, prometheus.NewCounterFunc(prometheus.CounterOpts{
		Namespace: "flock_agent",
		Subsystem: "stores",
		Name:      "number_resources",
		ConstLabels: map[string]string{
			"resource": helpers.TypeUrl[*cert.Certificate](),
		},
		Help: "Number of resources stored",
	}, func() float64 {
		return float64(LenStore[*cert.Certificate]())
	}))
}

// CountResourcesOf makes the number_resources metrics count the resources of sm.
func CountResourcesOf(sm storers.StoreManager) {
	countedMutex.Lock()
	defer countedMutex.Unlock()
	counted = sm
}

// StoreBy gives the store of T in sm, a type sm has no store for is a programming error.
func StoreBy[T proto.Message](sm storers.StoreManager) storers.Storer[T] {
	store, err := storers.StoreBy[T](sm)
	if err != nil {
		panic(err)
	}
	return store
}

// LenStore is read by the number_resources counters, which Prometheus calls with no ctx: it is the root of its read
func LenStore[T proto.Message]() int {
	ctx, cancel := context.WithTimeout(context.Background(), countTimeout)
	defer cancel()
	countedMutex.RLock()
	sm := counted
	countedMutex.RUnlock()
	if sm == nil {
		return 0
	}
	elems, err := StoreBy[T](sm).List(ctx, nil)
	if err != nil {
		return 0
	}
	return len(elems)
}
