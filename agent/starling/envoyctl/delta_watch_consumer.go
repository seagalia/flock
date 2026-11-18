package envoyctl

import (
	"context"
	"sync"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	listenerv3 "github.com/envoyproxy/go-control-plane/envoy/config/listener/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/stream/v3"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	nbWorkers = 6
	// more than the watches open at once, one per resource type, so a send to a watch never finds it full
	respChanSize = 30
)

type DeltaWatchConsumer struct {
	entry     zerolog.Logger
	confWatch cache.ConfigWatcher
}

func NewDeltaWatchConsumer(confWatch cache.ConfigWatcher) *DeltaWatchConsumer {
	return &DeltaWatchConsumer{
		entry:     logging.Component("delta_watch_consumer"),
		confWatch: confWatch,
	}
}

func (d *DeltaWatchConsumer) Run(ctx context.Context) error {
	// never closed since the config watcher may still send to a watch after the stop, the workers stop on ctx instead
	respChan := make(chan cache.DeltaResponse, respChanSize)

	wg := &sync.WaitGroup{}
	for range nbWorkers {
		wg.Go(func() {
			d.consume(ctx, respChan)
		})
	}
	d.watch(helpers.TypeUrl[*endpointv3.ClusterLoadAssignment](), respChan)
	d.watch(helpers.TypeUrl[*listenerv3.Listener](), respChan)
	d.watch(helpers.TypeUrl[*clusterv3.Cluster](), respChan)
	d.watch(helpers.TypeUrl[*tlsv3.Secret](), respChan)

	<-ctx.Done()
	d.entry.Info().Msg("Closing delta watch consumer")
	wg.Wait()
	d.entry.Info().Msg("Delta watch consumer stopped")
	return nil
}

func (d *DeltaWatchConsumer) consume(ctx context.Context, respChan chan cache.DeltaResponse) {
	for {
		select {
		case <-ctx.Done():
			return
		case resp := <-respChan:
			d.logResponse(resp)
			d.watch(resp.GetDeltaRequest().TypeUrl, respChan)
		}
	}
}

func (d *DeltaWatchConsumer) logResponse(resp cache.DeltaResponse) {
	entry := d.entry.With().Interface("request", resp.GetDeltaRequest()).Str("type", resp.GetDeltaRequest().TypeUrl).Logger()
	respDelta, err := resp.GetDeltaDiscoveryResponse()
	if err != nil {
		entry.Error().Err(err).Msg("Got error while getting delta response")
		return
	}
	b, err := protojson.MarshalOptions{
		Multiline:       true,
		Indent:          "  ",
		AllowPartial:    true,
		UseProtoNames:   true,
		UseEnumNumbers:  true,
		EmitUnpopulated: true,
	}.Marshal(respDelta)
	if err != nil {
		entry.Error().AnErr("error", err).Msg("Got error while marshalling response")
		return
	}
	entry.Info().Str("type", "envoy_response").Msg(string(b))
}

func (d *DeltaWatchConsumer) watch(typeUrl string, respChan chan cache.DeltaResponse) {
	_, err := d.confWatch.CreateDeltaWatch(&cache.DeltaRequest{TypeUrl: typeUrl}, stream.Subscription{}, respChan)
	if err != nil {
		d.entry.Error().Str("type", typeUrl).Err(err).Msg("Could not create delta watch")
	}
}
