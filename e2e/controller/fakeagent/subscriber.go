//go:build e2e

package fakeagent

import (
	"context"
	"crypto/tls"
	"github.com/seagalia/flock/sdk/agentsdk/waiters"
	"maps"
	"sync"

	"github.com/seagalia/flock/sdk/agentsdk/aggregaters"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/cert/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

// SubscriberTypeUrls are the resource types the real flock-agent subscribes to
var SubscriberTypeUrls = []string{
	helpers.TypeUrl[*vip.VIP](),
	helpers.TypeUrl[*vs.VirtualServer](),
	helpers.TypeUrl[*backend.Backend](),
	helpers.TypeUrl[*cert.Certificate](),
}

// streamChanCapacity mirrors the buffering of the real agent's ads handlers
const streamChanCapacity = 100

// Subscriber speaks to controller like flock-agent does on the wire, and records what controller sends
type Subscriber struct {
	instanceUuid string
	tlsConf      *tls.Config

	mutex      sync.Mutex
	state      map[string]map[string]*svcdiscovery.Resource
	history    map[string][]*svcdiscovery.DeltaDiscoveryResponse
	lastErr    error
	aggregator *aggregaters.Aggregator
	cancel     context.CancelFunc
	dropped    chan struct{}
	streams    sync.WaitGroup
}

func New(instanceUuid string, tlsConf *tls.Config) *Subscriber {
	return &Subscriber{
		instanceUuid: instanceUuid,
		tlsConf:      tlsConf,
		state:        make(map[string]map[string]*svcdiscovery.Resource),
		history:      make(map[string][]*svcdiscovery.DeltaDiscoveryResponse),
	}
}

// Connect replaces any previous connection with one stream per type url (all SubscriberTypeUrls when none given)
// and returns once each has sent its first request. ctx bounds the life of the streams; their later errors go to Err.
func (m *Subscriber) Connect(ctx context.Context, addr string, typeUrls ...string) error {
	m.Drop()
	if len(typeUrls) == 0 {
		typeUrls = SubscriberTypeUrls
	}
	node := &core.Node{
		ClientType:    "fakeagent",
		ClientVersion: &core.SemanticVersion{Major: 0, Minor: 0, Patch: 1},
	}
	aggregator := aggregaters.NewAggregator(addr, m.instanceUuid, node, m.tlsConf, waiters.NewSuspendWaiter())
	connCtx, cancel := context.WithCancel(ctx)
	dropped := make(chan struct{})

	m.mutex.Lock()
	m.aggregator = aggregator
	m.cancel = cancel
	m.dropped = dropped
	m.lastErr = nil
	m.mutex.Unlock()

	openedChans := make([]chan struct{}, len(typeUrls))
	failedChans := make([]chan error, len(typeUrls))
	for i, typeUrl := range typeUrls {
		openedChans[i] = make(chan struct{})
		failedChans[i] = make(chan error, 1)
		m.openStream(connCtx, dropped, aggregator, typeUrl, openedChans[i], failedChans[i])
	}
	for i := range typeUrls {
		select {
		case <-openedChans[i]:
		case err := <-failedChans[i]:
			// a refusal after the first request is a stream error for Err, not a failure to connect
			if isClosed(openedChans[i]) {
				continue
			}
			m.Drop()
			return err
		case <-ctx.Done():
			m.Drop()
			return ctx.Err()
		}
	}
	return nil
}

func (m *Subscriber) openStream(
	connCtx context.Context, dropped chan struct{}, aggregator *aggregaters.Aggregator, typeUrl string,
	opened chan struct{}, failed chan error,
) {
	streamCtx, endStream := context.WithCancel(connCtx)
	responses := make(chan *svcdiscovery.DeltaDiscoveryResponse, streamChanCapacity)
	acks := make(chan *svcdiscovery.DeltaDiscoveryRequest, streamChanCapacity)
	var openOnce sync.Once
	initialVersions := func() map[string]string {
		versions := m.heldVersions(typeUrl)
		// the aggregator calls this right before sending the first request, after dialing
		openOnce.Do(func() { close(opened) })
		return versions
	}

	m.streams.Add(2)
	go func() {
		defer m.streams.Done()
		defer endStream()
		err := aggregator.DeltaAggregatedResources(streamCtx, typeUrl, nil, initialVersions, responses, acks, nil)
		// closing the connection on Drop surfaces as an error, it is not a refusal from controller
		droppedByUs := isClosed(dropped) || streamCtx.Err() != nil
		if err == nil || droppedByUs {
			return
		}
		m.mutex.Lock()
		m.lastErr = err
		m.mutex.Unlock()
		failed <- err
	}()
	go func() {
		defer m.streams.Done()
		for {
			select {
			case <-streamCtx.Done():
				return
			case resp := <-responses:
				m.record(typeUrl, resp)
				ack := &svcdiscovery.DeltaDiscoveryRequest{
					TypeUrl:       typeUrl,
					InstanceUuid:  m.instanceUuid,
					ResponseNonce: resp.GetResponseNonce(),
				}
				select {
				case acks <- ack:
				case <-streamCtx.Done():
					return
				}
			}
		}
	}()
}

func (m *Subscriber) record(typeUrl string, resp *svcdiscovery.DeltaDiscoveryResponse) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.history[typeUrl] = append(m.history[typeUrl], resp)
	held, ok := m.state[typeUrl]
	if !ok {
		held = make(map[string]*svcdiscovery.Resource)
		m.state[typeUrl] = held
	}
	for _, res := range resp.GetResources() {
		held[res.GetUuid()] = res
	}
	for _, uuid := range resp.GetRemovedResources() {
		delete(held, uuid)
	}
}

// heldVersions is uuid -> signature, what the real agent's ads handler sends as initial versions
func (m *Subscriber) heldVersions(typeUrl string) map[string]string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	versions := make(map[string]string)
	for uuid, res := range m.state[typeUrl] {
		versions[uuid] = res.GetSignature()
	}
	return versions
}

// Drop closes the connection like a crash, it returns once nothing is recorded anymore
func (m *Subscriber) Drop() {
	m.mutex.Lock()
	aggregator := m.aggregator
	cancel := m.cancel
	dropped := m.dropped
	m.aggregator = nil
	m.cancel = nil
	m.dropped = nil
	m.mutex.Unlock()
	if aggregator == nil {
		return
	}
	close(dropped)
	// closing the connection before cancelling: a cancelled stream half-closes gracefully,
	// controller would see io.EOF where a crashed agent gives a transport error
	_ = aggregator.Close()
	cancel()
	m.streams.Wait()
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (m *Subscriber) ForgetState() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.state = make(map[string]map[string]*svcdiscovery.Resource)
}

// State is uuid -> last resource received for typeUrl
func (m *Subscriber) State(typeUrl string) map[string]*svcdiscovery.Resource {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return maps.Clone(m.state[typeUrl])
}

func (m *Subscriber) History(typeUrl string) []*svcdiscovery.DeltaDiscoveryResponse {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return append([]*svcdiscovery.DeltaDiscoveryResponse{}, m.history[typeUrl]...)
}

func (m *Subscriber) ClearHistory() {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.history = make(map[string][]*svcdiscovery.DeltaDiscoveryResponse)
}

// Err is the last error a stream ended with, like a refusal from controller
func (m *Subscriber) Err() error {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.lastErr
}
