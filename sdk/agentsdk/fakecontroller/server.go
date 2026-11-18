package fakecontroller

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/grpchealth/v2"
	svcdiscovery "github.com/seagalia/flock/sdk/flock/services/discovery/v1"
	"github.com/seagalia/flock/sdk/flock/services/discovery/v1/svcdiscoveryconnect"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/rpcserver"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

type Server struct {
	mutex *sync.Mutex
	// broadcast on every state, stream or ack change
	changed *sync.Cond
	// instance -> type url -> uuid -> resource
	state   map[string]map[string]map[string]*svcdiscovery.Resource
	streams map[*stream]struct{}
	nonce   uint64
	http    *http.Server
	// instances whose streams are refused, like an unreachable controller
	outages  map[string]bool
	refusals []refusal
	// agent name -> agent uuid, the name being the CN of the agent client certificate
	agents map[string]string
}

type refusal struct {
	instance string
	typeUrl  string
	at       time.Time
}

// stream is one agent subscription for one resource type
type stream struct {
	instance string
	typeUrl  string
	// uuid -> signature as known by the agent
	known map[string]string
	// nonce of the response waiting for an ack, empty when none
	inFlight string
	// first response must be sent even if there is nothing to send
	sentOnce bool
	wake     chan struct{}
	// closed to end the stream like a controller restart would
	drop chan struct{}
}

func New(tlsConf *tls.Config) *Server {
	mutex := &sync.Mutex{}
	s := &Server{
		mutex:   mutex,
		changed: sync.NewCond(mutex),
		state:   make(map[string]map[string]map[string]*svcdiscovery.Resource),
		streams: make(map[*stream]struct{}),
		outages: make(map[string]bool),
		agents:  make(map[string]string),
	}
	// the discovery stream checks the client certificate itself
	connectServer := rpcserver.NewServer(func(ctx context.Context, _ string) (context.Context, error) {
		return ctx, nil
	})
	svcdiscoveryconnect.RegisterAggregatedDiscoveryHandler(connectServer, s)
	mux := http.NewServeMux()
	rpcserver.Mount(mux, connectServer, grpchealth.NewStaticChecker())
	s.http = &http.Server{Handler: mux, TLSConfig: tlsConf, HTTP2: rpcserver.HTTP2Config()}
	return s
}

func (s *Server) Serve(lis net.Listener) error {
	return s.http.ServeTLS(lis, "", "")
}

func (s *Server) Stop() {
	_ = s.http.Close() // nolint:errcheck
}

// DeclareAgent makes the fake answer uuid to the streams of a client whose certificate has name as CN, like a
// controller declaring that agent. A CN no agent is declared with is refused.
func (s *Server) DeclareAgent(name, uuid string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.agents[name] = uuid
}

// Push creates or updates resources
func (s *Server) Push(instance string, resources ...proto.Message) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, message := range resources {
		anyRes, err := anypb.New(message)
		if err != nil {
			panic(err)
		}
		sig, err := helpers.MessageSignature(message)
		if err != nil {
			panic(err)
		}
		typeUrl := helpers.TypeUrlFromMessage(message)
		s.typeStateLocked(instance, typeUrl)[helpers.GetIdentifier(message)] = &svcdiscovery.Resource{
			Uuid:      helpers.GetIdentifier(message),
			Signature: sig,
			Resource:  anyRes,
		}
	}
	s.notifyLocked(instance)
}

func (s *Server) Remove(instance, typeUrl, uuid string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.typeStateLocked(instance, typeUrl), uuid)
	s.notifyLocked(instance)
}

func (s *Server) RemoveAll(instance string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.state, instance)
	s.notifyLocked(instance)
}

// WaitAcked returns once every stream of the instance acked the current state
func (s *Server) WaitAcked(ctx context.Context, instance string) error {
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-ctx.Done():
			s.mutex.Lock()
			s.changed.Broadcast()
			s.mutex.Unlock()
		case <-stopWatch:
		}
	}()

	s.mutex.Lock()
	defer s.mutex.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		synced := true
		for st := range s.streams {
			if st.instance != instance {
				continue
			}
			isWaitingAck := st.inFlight != ""
			hasUnsentState := !st.sentOnce || s.hasDiffLocked(st)
			if isWaitingAck || hasUnsentState {
				synced = false
				break
			}
		}
		if synced {
			return nil
		}
		s.changed.Wait()
	}
}

// DropStreams acts like a controller restart, the agent reconnects by itself
func (s *Server) DropStreams(instance string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for st := range s.streams {
		if st.instance == instance {
			close(st.drop)
			// a dropped stream must not be dropped twice nor count as subscribed while it stops
			delete(s.streams, st)
		}
	}
	s.changed.Broadcast()
}

// StartOutage drops the instance streams and refuses new ones until EndOutage
func (s *Server) StartOutage(instance string) {
	s.mutex.Lock()
	s.outages[instance] = true
	s.mutex.Unlock()
	s.DropStreams(instance)
}

func (s *Server) EndOutage(instance string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.outages, instance)
}

// Refusals lists the reconnection attempts refused during outages, oldest first
func (s *Server) Refusals(instance, typeUrl string) []time.Time {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	attempts := make([]time.Time, 0)
	for _, refusal := range s.refusals {
		if refusal.instance == instance && refusal.typeUrl == typeUrl {
			attempts = append(attempts, refusal.at)
		}
	}
	return attempts
}

func (s *Server) Subscribed(instance string, typeUrls ...string) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, typeUrl := range typeUrls {
		found := false
		for st := range s.streams {
			if st.instance == instance && st.typeUrl == typeUrl {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return len(typeUrls) > 0
}

func (s *Server) DeltaAggregatedResources(ctx context.Context, srv svcdiscoveryconnect.AggregatedDiscoveryDeltaAggregatedResourcesServerStream) error {
	first, err := srv.Receive()
	if err != nil {
		return err
	}
	if s.refuse(first.GetInstanceUuid(), first.GetTypeUrl()) {
		return connect.NewError(connect.CodeUnavailable, "fake controller outage")
	}
	agentUuid, err := s.identify(ctx)
	if err != nil {
		return err
	}
	callInfo, _ := connect.CallInfoForServerContext(ctx)
	callInfo.ResponseHeader().Set(helpers.HeaderAgentUuid, agentUuid)
	// same as the controller, the header holding the uuid is delivered by a first response with no nonce
	err = srv.Send(&svcdiscovery.DeltaDiscoveryResponse{})
	if err != nil {
		return err
	}
	st := &stream{
		instance: first.GetInstanceUuid(),
		typeUrl:  first.GetTypeUrl(),
		known:    make(map[string]string),
		wake:     make(chan struct{}, 1),
		drop:     make(chan struct{}),
	}
	maps.Copy(st.known, first.GetInitialResourceVersions())
	s.mutex.Lock()
	s.streams[st] = struct{}{}
	s.changed.Broadcast()
	s.mutex.Unlock()
	defer func() {
		s.mutex.Lock()
		delete(s.streams, st)
		s.changed.Broadcast()
		s.mutex.Unlock()
	}()

	recvErr := make(chan error, 1)
	go func() {
		for {
			req, err := srv.Receive()
			if err != nil {
				recvErr <- err
				return
			}
			s.mutex.Lock()
			if req.GetResponseNonce() != "" && req.GetResponseNonce() == st.inFlight {
				st.inFlight = ""
				s.changed.Broadcast()
				st.notify()
			}
			s.mutex.Unlock()
		}
	}()

	st.notify()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-recvErr:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case <-st.drop:
			return connect.NewError(connect.CodeUnavailable, "stream dropped by fake controller")
		case <-st.wake:
		}
		resp := s.nextResponse(st)
		if resp == nil {
			continue
		}
		if err := srv.Send(resp); err != nil {
			return err
		}
	}
}

// identify gives the uuid of the agent declared with the CN of the client certificate
func (s *Server) identify(ctx context.Context) (string, error) {
	client, _ := rpcserver.PeerFromContext(ctx)
	hasCertificate := client.TLS != nil && len(client.TLS.PeerCertificates) > 0
	if !hasCertificate {
		return "", connect.NewError(connect.CodePermissionDenied, "a client certificate is required")
	}
	commonName := client.TLS.PeerCertificates[0].Subject.CommonName
	s.mutex.Lock()
	defer s.mutex.Unlock()
	agentUuid, declared := s.agents[commonName]
	if !declared {
		return "", connect.Errorf(connect.CodePermissionDenied, "no agent named %q is declared", commonName)
	}
	return agentUuid, nil
}

func (s *Server) refuse(instance, typeUrl string) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if !s.outages[instance] {
		return false
	}
	s.refusals = append(s.refusals, refusal{instance: instance, typeUrl: typeUrl, at: time.Now()})
	return true
}

// nextResponse is nil when the stream misses nothing
func (s *Server) nextResponse(st *stream) *svcdiscovery.DeltaDiscoveryResponse {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if st.inFlight != "" {
		return nil
	}
	if st.sentOnce && !s.hasDiffLocked(st) {
		return nil
	}
	current := s.state[st.instance][st.typeUrl]
	resp := &svcdiscovery.DeltaDiscoveryResponse{}
	for uuid, res := range current {
		if st.known[uuid] == res.GetSignature() {
			continue
		}
		resp.Resources = append(resp.Resources, res)
		st.known[uuid] = res.GetSignature()
	}
	for uuid := range st.known {
		if _, ok := current[uuid]; ok {
			continue
		}
		resp.RemovedResources = append(resp.RemovedResources, uuid)
		delete(st.known, uuid)
	}
	s.nonce++
	resp.ResponseNonce = strconv.FormatUint(s.nonce, 10)
	st.inFlight = resp.ResponseNonce
	st.sentOnce = true
	s.changed.Broadcast()
	return resp
}

func (s *Server) hasDiffLocked(st *stream) bool {
	current := s.state[st.instance][st.typeUrl]
	if len(current) != len(st.known) {
		return true
	}
	for uuid, res := range current {
		if st.known[uuid] != res.GetSignature() {
			return true
		}
	}
	return false
}

func (s *Server) typeStateLocked(instance, typeUrl string) map[string]*svcdiscovery.Resource {
	if _, ok := s.state[instance]; !ok {
		s.state[instance] = make(map[string]map[string]*svcdiscovery.Resource)
	}
	if _, ok := s.state[instance][typeUrl]; !ok {
		s.state[instance][typeUrl] = make(map[string]*svcdiscovery.Resource)
	}
	return s.state[instance][typeUrl]
}

func (s *Server) notifyLocked(instance string) {
	for st := range s.streams {
		if st.instance == instance {
			st.notify()
		}
	}
	s.changed.Broadcast()
}

func (st *stream) notify() {
	select {
	case st.wake <- struct{}{}:
	default:
	}
}
