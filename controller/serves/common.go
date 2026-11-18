package serves

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/seagalia/flock/sdk/contexes"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"github.com/seagalia/flock/sdk/rpcserver"
	"github.com/seagalia/flock/sdk/storers"
	"google.golang.org/protobuf/proto"
)

const (
	// DefaultLimit is the default limit for list requests
	DefaultLimit  = 50
	AsUserMetaKey = "as_user"

	// detachedTimeout bounds the work a call leaves behind, cleanups after a failure and spreads of permissions
	detachedTimeout = 30 * time.Second

	// agentReadyTimeout bounds the time a lookup spends on a dead agent before trying the next one,
	// while leaving enough time for a slow TCP+TLS handshake to a live one
	agentReadyTimeout = 2 * time.Second
)

// detachedContext keeps the values of ctx without its cancellation, so a client gone after a partial change doesn't
// leave it half undone
func detachedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), detachedTimeout)
}

type reqListOptions interface {
	GetOffset() int32
	GetLimit() int32
}

func (s *FlockServer) makeListOptions(ctx context.Context, reqOpt reqListOptions) *storers.ListOptions {
	limit := reqOpt.GetLimit()
	offset := reqOpt.GetOffset()
	if limit == 0 {
		limit = DefaultLimit
	}
	username, groups, _ := contexes.RetrieveUserInfo(ctx)
	isAdmin := s.userIsAdmin(ctx, username, groups)
	if isAdmin {
		var isUsurping bool
		username, groups, isUsurping = s.retrieveAsUser(ctx)
		if !isUsurping {
			username = ""
			groups = nil
		}
	}
	return &storers.ListOptions{
		Limit:      int(limit),
		Offset:     int(offset),
		UserName:   username,
		UserGroups: groups,
	}
}

func (s *FlockServer) retrieveAsUser(ctx context.Context) (string, []string, bool) {
	asUsers := requestHeaderValues(ctx, AsUserMetaKey)
	if len(asUsers) == 0 {
		return "", []string{}, false
	}
	asUser := struct {
		Username string   `json:"username"`
		Groups   []string `json:"groups"`
	}{}
	err := json.Unmarshal([]byte(asUsers[0]), &asUser)
	if err != nil {
		s.entry.Error().Msgf("failed to unmarshal as_user header: %v", err)
		return "", []string{}, false
	}
	return asUser.Username, asUser.Groups, true

}

// requestHeaderValues gives the values of a request header of the call served with ctx
func requestHeaderValues(ctx context.Context, key string) []string {
	callInfo, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return nil
	}
	return callInfo.RequestHeader().Values(key)
}

// clientAddr gives the address of the client of the call served with ctx, empty when unknown
func clientAddr(ctx context.Context) string {
	client, _ := rpcserver.PeerFromContext(ctx)
	return client.Addr
}

// firstReachableAgent gives the first of agents answering
func (s *FlockServer) firstReachableAgent(ctx context.Context, agents []*agent.Agent) (*agent.Agent, error) {
	err := fmt.Errorf("no agent to connect to")
	for _, agentListener := range agents {
		ctxTimeout, cancelFunc := context.WithTimeout(ctx, agentReadyTimeout)
		isReachable := s.agentClients.IsReachable(ctxTimeout, agentListener.GetHost())
		cancelFunc()
		if isReachable {
			return agentListener, nil
		}
		err = fmt.Errorf("agent %s at %s is not reachable", agentListener.GetUUID(), agentListener.GetHost())
	}
	return nil, connect.Errorf(
		connect.CodeInternal,
		"failed to connect to a working agentListener, error: %s", err.Error(),
	)
}

func (s *FlockServer) filterAgentConnectable(ctx context.Context, agents []*agent.Agent) ([]*agent.Agent, error) {
	ag, err := s.firstReachableAgent(ctx, agents)
	if err != nil {
		return nil, err
	}
	return []*agent.Agent{ag}, nil
}

func isBridgeAgent(ag *agent.Agent) bool {
	return ag.GetMode() == agent.Agent_BRIDGE
}

func isProvisioner(ag *agent.Agent) bool {
	return ag.GetMode() == agent.Agent_PROVISIONER
}

func withoutProvisioners(agents []*agent.Agent) []*agent.Agent {
	return slices.DeleteFunc(slices.Clone(agents), isProvisioner)
}

func onlyProvisioners(agents []*agent.Agent) []*agent.Agent {
	return slices.DeleteFunc(slices.Clone(agents), func(ag *agent.Agent) bool { return !isProvisioner(ag) })
}

// isBridgeAgents looks at the load balancer agents only, a provisioner sorting first must not hide a bridge
func isBridgeAgents(agents []*agent.Agent) bool {
	balancers := withoutProvisioners(agents)
	if len(balancers) == 0 {
		return false
	}
	return isBridgeAgent(balancers[0])
}

// agentsToAsk narrows the agents of a bridge instance to one reachable bridge agent, provisioners are always asked
func (s *FlockServer) agentsToAsk(ctx context.Context, agents []*agent.Agent) ([]*agent.Agent, error) {
	if !isBridgeAgents(agents) {
		return agents, nil
	}
	reachable, err := s.filterAgentConnectable(ctx, withoutProvisioners(agents))
	if err != nil {
		return nil, err
	}
	return append(reachable, onlyProvisioners(agents)...), nil
}

func checkNameChangedByOwner[T proto.Message](ctx context.Context, storeMan storers.StoreManager, current T, checkPerm func() error) error {
	st, err := storers.StoreBy[T](storeMan)
	if err != nil {
		return err
	}
	old, err := st.Read(ctx, helpers.GetIdentifier(current))
	if err != nil {
		return err
	}
	if helpers.GetName(old) == helpers.GetName(current) {
		return nil
	}
	return checkPerm()
}
