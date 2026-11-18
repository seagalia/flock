//go:build e2e

package fakeagent

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"

	"connectrpc.com/grpchealth/v2"
	agent "github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	agentserv "github.com/seagalia/flock/sdk/flock/services/agent/logs/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/logs/v1/agentlogsconnect"
	agentstatus "github.com/seagalia/flock/sdk/flock/services/agent/status/v1"
	"github.com/seagalia/flock/sdk/flock/services/agent/status/v1/agentstatusconnect"
	"github.com/seagalia/flock/sdk/rpcserver"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Agent is the server side of an agent that controller dials for logs, statuses and metrics, with scripted answers
type Agent struct {
	httpServer *http.Server

	mutex         sync.Mutex
	latestLogs    []string
	followers     map[*follower]struct{}
	status        *agentstatus.StatusResponse
	listStatus    *agentstatus.ListStatusResponse
	resourceTypes []string
	agentStatus   *agent.AgentStatus
	metrics       string
}

type follower struct {
	lines chan string
	done  <-chan struct{}
}

func NewAgent(tlsConf *tls.Config) *Agent {
	a := &Agent{
		followers:   make(map[*follower]struct{}),
		status:      &agentstatus.StatusResponse{},
		listStatus:  &agentstatus.ListStatusResponse{},
		agentStatus: &agent.AgentStatus{},
	}
	// clients are authenticated by tlsConf, which requires their certificate
	connectServer := rpcserver.NewServer(func(ctx context.Context, _ string) (context.Context, error) {
		return ctx, nil
	})
	agentlogsconnect.RegisterLogsHandler(connectServer, &logsService{agent: a})
	agentstatusconnect.RegisterResourceStatusHandler(connectServer, &resourceStatusService{agent: a})
	agentstatusconnect.RegisterAgentStatusHandler(connectServer, &agentStatusService{agent: a})

	mux := http.NewServeMux()
	rpcserver.Mount(mux, connectServer, grpchealth.NewStaticChecker())
	mux.HandleFunc("/metrics", a.serveMetrics)
	a.httpServer = &http.Server{Handler: mux, TLSConfig: tlsConf, HTTP2: rpcserver.HTTP2Config()}
	return a
}

// Serve blocks until Stop
func (a *Agent) Serve(lis net.Listener) error {
	err := a.httpServer.ServeTLS(lis, "", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (a *Agent) Stop() {
	_ = a.httpServer.Close()
}

func (a *Agent) SetLatestLogs(lines ...string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.latestLogs = lines
}

// EmitLog sends line to every open Follow stream
func (a *Agent) EmitLog(line string) {
	a.mutex.Lock()
	followers := make([]*follower, 0, len(a.followers))
	for f := range a.followers {
		followers = append(followers, f)
	}
	a.mutex.Unlock()
	for _, f := range followers {
		select {
		case f.lines <- line:
		case <-f.done:
		}
	}
}

// Followers is the number of open Follow streams, to know when an EmitLog reaches a client
func (a *Agent) Followers() int {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	return len(a.followers)
}

func (a *Agent) SetStatus(resp *agentstatus.StatusResponse) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.status = resp
}

func (a *Agent) SetListStatus(resp *agentstatus.ListStatusResponse) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.listStatus = resp
}

func (a *Agent) SetResourceTypes(typeUrls ...string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.resourceTypes = typeUrls
}

func (a *Agent) SetAgentStatus(st *agent.AgentStatus) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.agentStatus = st
}

// SetMetrics sets the prometheus text served on /metrics
func (a *Agent) SetMetrics(text string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	a.metrics = text
}

func (a *Agent) serveMetrics(w http.ResponseWriter, _ *http.Request) {
	a.mutex.Lock()
	metrics := a.metrics
	a.mutex.Unlock()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(metrics))
}

type logsService struct {
	agentlogsconnect.UnimplementedLogsHandler
	agent *Agent
}

func (s *logsService) Latest(context.Context, *emptypb.Empty) (*agentserv.LogLatestResponse, error) {
	s.agent.mutex.Lock()
	defer s.agent.mutex.Unlock()
	return &agentserv.LogLatestResponse{LogLines: s.agent.latestLogs}, nil
}

func (s *logsService) Follow(ctx context.Context, _ *emptypb.Empty, stream agentlogsconnect.LogsFollowServerStream) error {
	f := &follower{lines: make(chan string), done: ctx.Done()}
	s.agent.mutex.Lock()
	s.agent.followers[f] = struct{}{}
	s.agent.mutex.Unlock()
	defer func() {
		s.agent.mutex.Lock()
		delete(s.agent.followers, f)
		s.agent.mutex.Unlock()
	}()
	started := &agentserv.LogStreamResponse{Event: &agentserv.LogStreamResponse_Started{Started: &agentserv.LogStreamResponse_StreamStarted{}}}
	if err := stream.Send(started); err != nil {
		return err
	}
	for {
		select {
		case <-f.done:
			return nil
		case line := <-f.lines:
			err := stream.Send(&agentserv.LogStreamResponse{Event: &agentserv.LogStreamResponse_LogLine{LogLine: line}})
			if err != nil {
				return err
			}
		}
	}
}

type resourceStatusService struct {
	agentstatusconnect.UnimplementedResourceStatusHandler
	agent *Agent
}

func (s *resourceStatusService) GetStatus(context.Context, *agentstatus.GetStatusRequest) (*agentstatus.StatusResponse, error) {
	s.agent.mutex.Lock()
	defer s.agent.mutex.Unlock()
	return proto.Clone(s.agent.status).(*agentstatus.StatusResponse), nil
}

func (s *resourceStatusService) ListStatus(context.Context, *agentstatus.ListStatusRequest) (*agentstatus.ListStatusResponse, error) {
	s.agent.mutex.Lock()
	defer s.agent.mutex.Unlock()
	return proto.Clone(s.agent.listStatus).(*agentstatus.ListStatusResponse), nil
}

func (s *resourceStatusService) ListResourceTypes(context.Context, *emptypb.Empty) (*agentstatus.ListResourceTypesResponse, error) {
	s.agent.mutex.Lock()
	defer s.agent.mutex.Unlock()
	return &agentstatus.ListResourceTypesResponse{ResourceTypes: s.agent.resourceTypes}, nil
}

type agentStatusService struct {
	agentstatusconnect.UnimplementedAgentStatusHandler
	agent *Agent
}

func (s *agentStatusService) GetAgentStatus(context.Context, *agentstatus.GetAgentStatusRequest) (*agent.AgentStatus, error) {
	s.agent.mutex.Lock()
	defer s.agent.mutex.Unlock()
	return proto.Clone(s.agent.agentStatus).(*agent.AgentStatus), nil
}
