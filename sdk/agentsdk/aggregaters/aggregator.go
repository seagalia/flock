package aggregaters

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/sdk/agentsdk/waiters"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/logging"
)

//go:generate go tool mockgen -destination=../testhelpers/mock_aggregator.go -package=testhelpers . Aggregater

const (
	connectTimeout  = 30 * time.Second
	sendStopTimeout = 5 * time.Second
)

// ErrClosed is returned by Connect once Close was called, an aggregator is not reused after it.
var ErrClosed = errors.New("aggregator closed")

type Aggregater interface {
	DeltaAggregatedResources(ctx context.Context, typeUrl string,
		filters svccore.ListFilters,
		initResourceFunc func() map[string]string,
		receiveResp chan *svcdiscovery.DeltaDiscoveryResponse,
		responseChan chan *svcdiscovery.DeltaDiscoveryRequest,
		startHook func(),
	) error
}

type Aggregator struct {
	tlsConf     *tls.Config
	addr        string
	grpcClients *helpers.GrpcClients
	adsClient   svcdiscoveryconnect.AggregatedDiscoveryClient
	// streamsCtx is the parent of every stream, Close cancels it to abort them as closing a connection would
	streamsCtx    context.Context
	cancelStreams context.CancelFunc
	mutex         *sync.Mutex
	instanceUuid  string
	closed        bool
	nbTry         int
	node          *core.Node
	entry         zerolog.Logger
	// agentUuid is given by the controller on the first stream carrying it, agentUuidKnown is closed once it is set
	agentUuid      string
	agentUuidKnown chan struct{}
	// waiter holds the received resources while the agent work is suspended
	waiter waiters.Waiter
}

func NewAggregator(addr, instanceUuid string, node *core.Node, tlsConf *tls.Config, waiter waiters.Waiter) *Aggregator {
	return &Aggregator{
		addr:    addr,
		nbTry:   3,
		tlsConf: tlsConf,
		mutex:   &sync.Mutex{},
		entry: logging.Component("aggregator").
			With().
			Str("instance_uuid", instanceUuid).
			Str("ads_server", addr).
			Logger(),
		instanceUuid:   instanceUuid,
		node:           node,
		agentUuidKnown: make(chan struct{}),
		waiter:         waiter,
	}
}

// AgentUuid is the uuid of the declared agent the controller identified, empty until a stream gave it
func (a *Aggregator) AgentUuid() string {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return a.agentUuid
}

// WaitAgentUuid returns the uuid of the declared agent once a stream gave it
func (a *Aggregator) WaitAgentUuid(ctx context.Context) (string, error) {
	select {
	case <-a.agentUuidKnown:
		return a.AgentUuid(), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// recordAgentUuid keeps the first uuid given by the controller, a later stream can't change the identity of a running
// agent
func (a *Aggregator) recordAgentUuid(entry zerolog.Logger, agentUuid string) {
	if agentUuid == "" {
		entry.Warn().Msg("the controller gave no agent uuid on the stream")
		return
	}
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.agentUuid == "" {
		a.agentUuid = agentUuid
		close(a.agentUuidKnown)
		return
	}
	if a.agentUuid != agentUuid {
		entry.Error().
			Str("agent_uuid", a.agentUuid).
			Str("received_agent_uuid", agentUuid).
			Msg("the controller gave another agent uuid, the first one is kept")
	}
}

func (a *Aggregator) SetNbTry(nbTry int) {
	a.nbTry = nbTry
}

// streams gives the client pool and the ctx Close cancels, created on the first connect
func (a *Aggregator) streams() (*helpers.GrpcClients, context.Context, error) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	if a.closed {
		return nil, nil, ErrClosed
	}
	if a.grpcClients == nil {
		a.grpcClients = helpers.NewGrpcClients(a.tlsConf)
		a.adsClient = svcdiscoveryconnect.NewAggregatedDiscoveryClient(a.grpcClients.Client(a.addr))
		a.streamsCtx, a.cancelStreams = context.WithCancel(context.Background())
	}
	return a.grpcClients, a.streamsCtx, nil
}

// Connect checks the server is reachable.
func (a *Aggregator) Connect(ctx context.Context) error {
	grpcClients, streamsCtx, err := a.streams()
	if err != nil {
		return err
	}

	// the check runs out of the lock and stops with Close, which a server that never answers would hold for the
	// whole connect timeout
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	stopWithClose := context.AfterFunc(streamsCtx, cancel)
	defer stopWithClose()
	isReachable := grpcClients.IsReachable(ctx, a.addr)
	if streamsCtx.Err() != nil {
		return ErrClosed
	}
	if !isReachable {
		return fmt.Errorf("could not connect to %s", a.addr)
	}
	a.entry.Info().Msg("connected to server")
	return nil
}

type recvResult struct {
	resp *svcdiscovery.DeltaDiscoveryResponse
	err  error
}

func (a *Aggregator) DeltaAggregatedResources(
	ctx context.Context, typeUrl string,
	filters svccore.ListFilters,
	initResourceFunc func() map[string]string,
	receiveResp chan *svcdiscovery.DeltaDiscoveryResponse,
	responseChan chan *svcdiscovery.DeltaDiscoveryRequest,
	startHook func(),
) error {
	entry := a.entry.With().Str("aggr_type_url", typeUrl).Logger()
	entry.Info().Msg("Create stream")

	if a.isContextDone(ctx) {
		return nil
	}
	err := a.Connect(ctx)
	if errors.Is(err, ErrClosed) {
		// Close ends the streams as a normal stop, the caller must not retry
		return nil
	}
	if a.isContextDone(ctx) {
		// the reachability check fails when ctx ends during it, a stop the caller must not take for a dead server
		return nil
	}
	if err != nil {
		stats.AddConnectFailure(typeUrl)
		return err
	}
	stats.AddConnectSuccess(typeUrl)

	a.mutex.Lock()
	adsClient := a.adsClient
	streamsCtx := a.streamsCtx
	a.mutex.Unlock()
	// not derived from ctx: the end of ctx closes the stream gracefully (CloseSend) instead of aborting it,
	// cancelling it on return unblocks the receiving goroutine
	streamCtx, cancelStream := context.WithCancel(streamsCtx)
	defer cancelStream()
	streamCtx, callInfo := connect.NewClientContext(streamCtx)
	adsStream, err := adsClient.DeltaAggregatedResources(streamCtx)
	if err != nil {
		return a.streamError(err)
	}
	err = adsStream.Send(&svcdiscovery.DeltaDiscoveryRequest{
		Node:                    a.node,
		TypeUrl:                 typeUrl,
		InstanceUuid:            a.instanceUuid,
		InitialResourceVersions: initResourceFunc(),
		Filters:                 filters,
	})
	if errors.Is(err, io.EOF) {
		// the stream was ended by the server, its real status is only given by Receive
		_, err = adsStream.Receive()
	}
	if err != nil {
		return a.streamError(err)
	}

	sendErrChan := make(chan error, 1)
	stopSend := make(chan struct{})
	sendDone := make(chan struct{})
	go func() {
		defer close(sendDone)
		a.sendRequests(entry, typeUrl, adsStream, responseChan, stopSend, sendErrChan)
	}()
	// a stream forbids CloseSend during a Send
	defer func() {
		close(stopSend)
		select {
		case <-sendDone:
		case <-time.After(sendStopTimeout):
			// a Send blocked by flow control only returns when the stream is cancelled
			cancelStream()
			<-sendDone
		}
		err := adsStream.CloseSend()
		entry.Debug().Err(err).Msg("close send")
		_ = adsStream.Close() // nolint:errcheck releases the stream, the call is over
	}()

	recvChan := make(chan recvResult)
	go receiveResponses(streamCtx, adsStream, recvChan)

	firstReceive := true
	firstForward := true
	for {
		var received recvResult
		select {
		case <-ctx.Done():
			return nil
		case <-streamCtx.Done():
			// while the loop runs only Close cancels it, the receiving goroutine may be gone without a result
			return nil
		case sendErr := <-sendErrChan:
			return a.streamError(sendErr)
		case received = <-recvChan:
		}
		if received.err != nil {
			return a.streamError(received.err)
		}
		resp := received.resp
		if firstReceive {
			firstReceive = false
			// the headers are read only once the first response arrived
			a.recordAgentUuid(entry, callInfo.ResponseHeader().Get(helpers.HeaderAgentUuid))
			isIdentityResponse := len(resp.GetResources()) == 0 && len(resp.GetRemovedResources()) == 0 &&
				resp.GetResponseNonce() == ""
			if isIdentityResponse {
				continue
			}
		}
		if firstForward && startHook != nil {
			startHook()
		}
		firstForward = false

		suspended, waitChan := a.waiter.ShouldWait()
		if suspended {
			entry.Debug().Msg("receive suspended")
			select {
			case <-waitChan:
			case <-ctx.Done():
				return nil
			case <-streamCtx.Done():
				return nil
			}
			entry.Debug().Msg("receive resumed")
		}
		entry.Debug().Msg("receive response: " + received.resp.LogSummary())
		a.waiter.AddActive()
		stats.AddReceive(typeUrl)
		select {
		case receiveResp <- received.resp:
		case <-ctx.Done():
		}
		a.waiter.RemoveActive()
	}
}

func (a *Aggregator) sendRequests(
	entry zerolog.Logger, typeUrl string, adsStream svcdiscoveryconnect.AggregatedDiscoveryDeltaAggregatedResourcesClientStream,
	responseChan chan *svcdiscovery.DeltaDiscoveryRequest,
	stopSend chan struct{}, sendErrChan chan error,
) {
	for {
		select {
		case <-stopSend:
			entry.Debug().Msg("stop sending request")
			return
		case req := <-responseChan:
			logEnt := entry.With().Str("res_type", req.GetTypeUrl()).Str("nonce", req.GetResponseNonce()).Logger()
			logEnt.Debug().Msg("Sending nonce ...")
			err := adsStream.Send(req)
			if errors.Is(err, io.EOF) {
				// the stream was ended by the server, the receiving goroutine gets its real status
				stats.AddRequestFailure(typeUrl)
				return
			}
			if err != nil {
				stats.AddRequestFailure(typeUrl)
				logEnt.Error().Err(err).Msg("failed to send request")
				sendErrChan <- err
				return
			}
			logEnt.Debug().Msg("Sending nonce done.")
			stats.AddRequestSuccess(typeUrl)
		}
	}
}

func receiveResponses(
	streamCtx context.Context,
	adsStream svcdiscoveryconnect.AggregatedDiscoveryDeltaAggregatedResourcesClientStream,
	recvChan chan recvResult,
) {
	for {
		resp, err := adsStream.Receive()
		select {
		case recvChan <- recvResult{resp: resp, err: err}:
		case <-streamCtx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

// streamError is nil for the end of a stream of a closed aggregator, Close aborts them
func (a *Aggregator) streamError(err error) error {
	a.mutex.Lock()
	closed := a.closed
	a.mutex.Unlock()
	if closed {
		return nil
	}
	return err
}

func (a *Aggregator) isContextDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
	}
	return false
}

func (a *Aggregator) Close() error {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.closed = true
	if a.grpcClients == nil {
		return nil
	}
	a.cancelStreams()
	a.grpcClients.Close()
	a.grpcClients = nil
	return nil
}
