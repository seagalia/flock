package serves

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/rs/zerolog"
	"github.com/seagalia/flock/controller/announcers"
	"github.com/seagalia/flock/controller/auth"
	"github.com/seagalia/flock/sdk/errors"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/rpcserver"
	"github.com/seagalia/flock/sdk/storers"
)

const waitForResponse = 3 * time.Minute

// DiscoveryStream is the server side of an agent discovery stream
// (svcdiscoveryconnect.AggregatedDiscoveryDeltaAggregatedResourcesServerStream)
type DiscoveryStream interface {
	Receive() (*svcdiscovery.DeltaDiscoveryRequest, error)
	Send(*svcdiscovery.DeltaDiscoveryResponse) error
}

var errDiscoveryStreamClosed = stderrors.New("discovery stream closed")

// guardedStream refuses sends once the handler serving the stream returned: announcers may still announce to a
// disconnected listener, and net/http panics on a write after the handler returned.
type guardedStream struct {
	DiscoveryStream
	mutex  sync.Mutex
	closed bool
}

func (g *guardedStream) Send(resp *svcdiscovery.DeltaDiscoveryResponse) error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	if g.closed {
		return errDiscoveryStreamClosed
	}
	return g.DiscoveryStream.Send(resp)
}

// close waits for a running Send
func (g *guardedStream) close() {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.closed = true
}

func (s *FlockServer) SetListenFactory(
	listFactory func(ctx context.Context, stream DiscoveryStream, done chan *DoneEmit, errEmit chan *ErrEmit) announcers.Listener,
) {
	s.listenFactory = listFactory
}

func (s *FlockServer) SetDoneChanFactory(doneChanFactory func() chan *DoneEmit) {
	s.doneChanFactory = doneChanFactory
}

func (s *FlockServer) SetErrEmitChanFactory(errEmitChanFactory func() chan *ErrEmit) {
	s.errEmitChanFactory = errEmitChanFactory
}

func (s *FlockServer) SetInitDiscoverer(initDiscoverer func(ctx context.Context, entry zerolog.Logger,
	stream DiscoveryStream,
	waitInit chan struct{}, done chan *DoneEmit) func(changes []*svcdiscovery.Resource, removes []string)) {
	s.initDiscoverer = initDiscoverer
}

func (s *FlockServer) DeltaAggregatedResources(ctx context.Context, connectStream svcdiscoveryconnect.AggregatedDiscoveryDeltaAggregatedResourcesServerStream) error {
	stream := &guardedStream{DiscoveryStream: connectStream}
	defer stream.close()
	_, ok := auth.IsMTlsConnection(ctx)
	if !ok {
		return connect.Errorf(connect.CodePermissionDenied, "Only controlled agent with mtls could connect")
	}
	firstIn, err := stream.Receive()
	if stderrors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	if firstIn.GetTypeUrl() == "" {
		return connect.Errorf(connect.CodeInvalidArgument, "type_url is required")
	}
	if firstIn.GetInstanceUuid() == "" {
		return connect.Errorf(connect.CodeInvalidArgument, "instance_uuid is required")
	}

	_, err = s.storeMan.Instances().Read(ctx, firstIn.GetInstanceUuid())
	if err != nil {
		return errors.ToConnectError(err)
	}
	entry := s.entry.With().
		Str("instance_uuid", firstIn.GetInstanceUuid()).
		Str("type_url", firstIn.GetTypeUrl()).
		Str("client_type", firstIn.GetNode().GetClientType()).
		Str("client_version", firstIn.GetNode().GetClientVersion().Format()).
		Logger()
	// the source IP of the connection, never a forwarded header the agent could set
	client, _ := rpcserver.PeerFromContext(ctx)
	addr, _, splitErr := net.SplitHostPort(client.Addr)
	if splitErr != nil {
		addr = client.Addr
	}
	entry = entry.With().Str("addr", addr).Logger()

	declared, err := s.identifyAgent(ctx, entry, firstIn.GetInstanceUuid())
	if err != nil {
		return err
	}
	entry = entry.With().
		Str("agent_name", declared.GetName()).
		Str("agent_uuid", declared.GetUUID()).
		Logger()
	callInfo, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return connect.Errorf(connect.CodeInternal, "the stream of agent %s has no call info to give its uuid in", declared.GetName())
	}
	key := nodeKey{instanceUuid: firstIn.GetInstanceUuid(), agentUuid: declared.GetUUID()}
	err = s.claimNode(ctx, entry, firstIn, declared, addr)
	if err != nil {
		return err
	}
	s.presence.open(ctx, key, addr)
	defer s.presence.close(ctx, key)

	callInfo.ResponseHeader().Set(helpers.HeaderAgentUuid, declared.GetUUID())
	// a first response delivers the header holding the uuid even when the stream has nothing to announce
	err = stream.Send(&svcdiscovery.DeltaDiscoveryResponse{})
	if err != nil {
		return err
	}
	entry.Info().Msg("Client connected")
	defer entry.Info().Msg("Client disconnected")

	waitInit := make(chan struct{}, 1)
	// done and errEmit are never closed: announcers may still call OnAnnounce after this
	// function returns, and a send on a closed channel panics.
	done := s.doneChanFactory()
	errEmit := s.errEmitChanFactory()
	anList := s.listenFactory(ctx, stream, done, errEmit)
	initCtx, stopInit := context.WithCancel(ctx)
	defer stopInit()
	stopInitWithServer := context.AfterFunc(s.ctx, stopInit)
	defer stopInitWithServer()
	err = s.linker.Connect(firstIn.GetInstanceUuid(), firstIn.GetTypeUrl(), anList,
		firstIn.GetInitialResourceVersions(),
		firstIn.GetFilters(),
		s.initDiscoverer(initCtx, entry, stream, waitInit, done),
	)
	// init uses the stream, it must be over before the handler returns: net/http panics on a write after that. It
	// stops waiting for the ack of the agent when the call or the server ends.
	<-waitInit
	entry.Debug().Msg("Init done")
	if err != nil {
		return connect.NewError(connect.CodeInternal, err.Error())
	}
	defer s.linker.Disconnect(firstIn.GetInstanceUuid(), firstIn.GetTypeUrl(), anList)
	select {
	case <-ctx.Done():
		entry.Debug().Msg("Stream context done")
		return nil
	case doneResult := <-done:
		if doneResult.Err != nil {
			if stderrors.Is(doneResult.Err, io.EOF) {
				return nil
			}
			entry.Error().Err(doneResult.Err).Msg("Error when done")
			return doneResult.Err
		}
	case errRec := <-errEmit:
		entry.Error().Err(errRec.Err).Msg("Error when sending resource to agent")
	case <-s.ctx.Done():
		entry.Debug().Msg("Server context done")
	}
	return nil
}

// identifyAgent gives the agent declared for the instance of the stream whose name is the common name of the client
// certificate
func (s *FlockServer) identifyAgent(ctx context.Context, entry zerolog.Logger, instanceUuid string) (*agent.Agent, error) {
	commonName, hasCertificate := auth.ClientCommonName(ctx)
	if !hasCertificate {
		entry.Warn().Msg("Refused an agent with no client certificate")
		return nil, connect.Errorf(connect.CodePermissionDenied,
			"a client certificate is required to connect for the instance %s", instanceUuid)
	}
	refused := connect.Errorf(connect.CodePermissionDenied, "no agent named %q is declared for the instance %s",
		commonName, instanceUuid)
	agents, err := s.instantiater.ListAgents(ctx, instanceUuid)
	if err != nil {
		entry.Warn().Err(err).Str("common_name", commonName).Msg("Refused an agent, the agents of the instance can't be listed")
		return nil, refused
	}
	for _, declared := range agents {
		if declared.GetName() == commonName {
			return declared, nil
		}
	}
	entry.Warn().Str("common_name", commonName).Msg("Refused an agent, no declared agent is named after its certificate")
	return nil, refused
}

// claimNode records the node of the agent connecting from addr
func (s *FlockServer) claimNode(ctx context.Context, entry zerolog.Logger, firstIn *svcdiscovery.DeltaDiscoveryRequest, declared *agent.Agent, addr string) error {
	instanceNode := &svclookup.InstanceNode{
		Node:      firstIn.GetNode(),
		AgentUuid: declared.GetUUID(),
		Name:      declared.GetName(),
	}
	err := s.storeMan.Nodes().ClaimNode(ctx, firstIn.GetInstanceUuid(), instanceNode, addr, nodeStaleAfter)
	if held, ok := stderrors.AsType[*storers.NodeHeldError](err); ok {
		entry.Warn().Str("held_from", held.Addr).Msg("Refused an agent already connected from another address")
		return connect.Errorf(connect.CodeFailedPrecondition,
			"agent %q of the instance %s is already connected from %s, refused from %s",
			declared.GetName(), firstIn.GetInstanceUuid(), held.Addr, addr)
	}
	if err != nil {
		return errors.ToConnectError(err)
	}
	return nil
}

func DefaultInitDiscoverer(ctx context.Context, entry zerolog.Logger, stream DiscoveryStream, waitInit chan struct{}, done chan *DoneEmit) func(changes []*svcdiscovery.Resource, removes []string) {
	return func(changes []*svcdiscovery.Resource, removes []string) {
		var initErr error
		defer func() {
			// waitInit is signalled before reporting the failure: the handler only reads done
			// after waitInit, and done may already be full because OnAnnounce runs concurrently.
			waitInit <- struct{}{}
			if initErr != nil {
				emitDone(ctx, done, &DoneEmit{Err: initErr})
			}
		}()
		if len(changes) == 0 && len(removes) == 0 {
			return
		}
		toResp := &svcdiscovery.DeltaDiscoveryResponse{
			RemovedResources: removes,
			Resources:        changes,
			ResponseNonce:    "0",
		}
		err := stream.Send(toResp)
		entry.Debug().Msgf("send response: %s", toResp.LogSummary())
		if err != nil {
			initErr = fmt.Errorf("error sending response: %w", err)
			return
		}
		waitRecv := make(chan struct{})
		var resp *svcdiscovery.DeltaDiscoveryRequest
		var recvErr error
		go func() {
			resp, recvErr = stream.Receive()
			close(waitRecv)
		}()
		select {
		case <-time.After(waitForResponse):
			initErr = connect.Errorf(connect.CodeDeadlineExceeded, "Wait for request nonce timed out")
			return
		case <-ctx.Done():
			// the call or the server ended, the handler stops on it with nothing to report
			return
		case <-waitRecv:
		}
		if recvErr != nil {
			initErr = fmt.Errorf("error receiving response: %w", recvErr)
			return
		}
		if resp.ResponseNonce != "0" {
			initErr = fmt.Errorf("invalid nonce: %s", resp.ResponseNonce)
			return
		}
		if resp.ErrorDetail != nil {
			initErr = connect.NewError(connect.Code(resp.ErrorDetail.GetCode()), resp.ErrorDetail.GetMessage())
		}
	}
}
