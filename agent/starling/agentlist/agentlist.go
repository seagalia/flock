package agentlist

import (
	"context"
	"crypto/sha256"
	stderrors "errors"
	"fmt"
	golog "log"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/rs/zerolog"
	"github.com/samber/lo"
	"github.com/seagalia/flock/agent/starling/logextends"
	"github.com/seagalia/flock/sdk/logging"
)

const (
	retryWait    = 30 * time.Second
	leaveTimeout = 5 * time.Second
)

type NodeEventListener interface {
	OnNodeJoin(*memberlist.Node)
	OnNodeLeave(*memberlist.Node)
	OnNodeUpdate(*memberlist.Node)
}

type AgentList struct {
	l              zerolog.Logger
	listeners      *sync.Map
	ml             *memberlist.Memberlist
	membersList    []string
	uptimeDelegate *UptimeDelegate

	notifyMutex sync.Mutex
	// leaving stops the events to the listeners, the leave of this node would make its placer drop every vip
	leaving bool
	// notifying holds the goroutines passing an event to the listeners
	notifying sync.WaitGroup
}

func New(nodeName, bindAddr string, bindPort uint16, secret string, membersList []string) (*AgentList, error) {
	entry := logging.Component("agentlist")
	sl := &AgentList{
		l:         entry,
		listeners: &sync.Map{},
	}

	mconfig := memberlist.DefaultLANConfig()

	mconfig.Name = nodeName
	mconfig.BindAddr = bindAddr
	if bindPort != 0 {
		mconfig.BindPort = int(bindPort)
		mconfig.AdvertisePort = int(bindPort)
	}
	mconfig.Logger = golog.New(&logextends.MemberlistWriter{Logger: logging.Component("memberlist")}, "", 0)
	if secret == "" {
		entry.Warn().Str("op", "startup").Msg("no ml-secret-key set, memberlist traffic will not be encrypted")
	} else {
		sha := sha256.New()
		mconfig.SecretKey = sha.Sum([]byte(secret))[:16]
	}
	sl.uptimeDelegate = NewUptimeDelegate()
	mconfig.Delegate = sl.uptimeDelegate
	mconfig.Events = sl

	ml, err := memberlist.Create(mconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create memberlist: %w", err)
	}

	// filter out our own IP addresses
	hostIps := []string{bindAddr}
	if bindAddr == "0.0.0.0" {
		hostIps, err = listAllInterfacesIps()
		if err != nil {
			return nil, fmt.Errorf("failed to list all interfaces IPs: %w", err)
		}
	}
	for i, addr := range hostIps {
		hostIps[i] = net.JoinHostPort(addr, fmt.Sprintf("%d", mconfig.BindPort))
	}

	stats.SetNodeInfo(nodeName, bindAddr)
	membersList = lo.Filter[string](membersList, func(host string, _ int) bool {
		spHost, port, err := net.SplitHostPort(host)
		if err != nil {
			spHost = host
			port = fmt.Sprintf("%d", mconfig.BindPort)
		}
		finalHost := net.JoinHostPort(spHost, port)

		addrs, err := net.LookupHost(spHost)
		if err != nil {
			entry.Error().Err(err).Msgf("failed to resolve member %s", spHost)
			addrs = []string{spHost}
		}
		stats.Disconnected(finalHost)
		for _, addr := range addrs {
			addr = net.JoinHostPort(addr, port)
			if lo.Contains[string](hostIps, addr) {
				stats.Connected(addr)
				return false
			}
		}
		return true
	})

	sl.membersList = membersList
	sl.ml = ml
	return sl, nil
}

func (sl *AgentList) Run(ctx context.Context) error {
	sl.autoJoin(ctx)
	<-ctx.Done()
	sl.stopNotifying()
	sl.l.Info().Msg("Leaving the agent list ...")
	leaveErr := sl.ml.Leave(leaveTimeout)
	if leaveErr != nil {
		leaveErr = fmt.Errorf("leave the agent list: %w", leaveErr)
	}
	shutdownErr := sl.ml.Shutdown()
	if shutdownErr != nil {
		shutdownErr = fmt.Errorf("shut down memberlist: %w", shutdownErr)
	}
	return stderrors.Join(leaveErr, shutdownErr)
}

func (sl *AgentList) autoJoin(ctx context.Context) {
	ticker := time.NewTicker(retryWait)
	defer ticker.Stop()
	joined := make(map[string]bool)
	for {
		sl.l.Info().Msg("Connecting to members ...")
		for _, member := range sl.membersList {
			if _, ok := joined[member]; ok {
				continue
			}
			entry := sl.l.With().Str("member", member).Logger()
			_, err := sl.ml.Join([]string{member})
			if err != nil {
				entry.Warn().
					Msgf("failed to join member %s, it could be normal if others node not started: %s", member, err.Error())
				continue
			}
			joined[member] = true
			entry.Info().Msg("connected.")
		}
		if len(joined) == len(sl.membersList) {
			return
		}
		sl.l.Warn().Msg("Not all members has been joined, retrying in 30 seconds ...")
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}

}

func (sl *AgentList) NotifyJoin(node *memberlist.Node) {
	sl.l.Debug().Msgf("node joined: %v", node.Name)
	stats.Connected(node.FullAddress().Addr)
	sl.notify(func(listener NodeEventListener) { listener.OnNodeJoin(node) })
}

func (sl *AgentList) NotifyLeave(node *memberlist.Node) {
	sl.l.Debug().Msgf("node leave: %v", node.Name)
	stats.Disconnected(node.FullAddress().Addr)
	sl.notify(func(listener NodeEventListener) { listener.OnNodeLeave(node) })
}

func (sl *AgentList) NotifyUpdate(node *memberlist.Node) {
	sl.notify(func(listener NodeEventListener) { listener.OnNodeUpdate(node) })
}

func (sl *AgentList) notify(event func(NodeEventListener)) {
	sl.notifyMutex.Lock()
	defer sl.notifyMutex.Unlock()
	if sl.leaving {
		return
	}
	sl.notifying.Go(func() {
		sl.listeners.Range(func(_, value any) bool {
			event(value.(NodeEventListener))
			return true
		})
	})
}

// Leaving tells if the agent started leaving the cluster.
func (sl *AgentList) Leaving() bool {
	sl.notifyMutex.Lock()
	defer sl.notifyMutex.Unlock()
	return sl.leaving
}

// stopNotifying drops the next events and waits for the ones being passed, so no placer changes the vips once the stop
// began, nor races the clean of the handlers
func (sl *AgentList) stopNotifying() {
	sl.notifyMutex.Lock()
	sl.leaving = true
	sl.notifyMutex.Unlock()
	sl.notifying.Wait()
}

func (sl *AgentList) Members() []*memberlist.Node {
	return sl.ml.Members()
}

func (sl *AgentList) AddNodeEventListener(listener NodeEventListener) {
	sl.listeners.Store(fmt.Sprintf("%p", listener), listener)
}

func (sl *AgentList) RemoveNodeEventListener(listener NodeEventListener) {
	sl.listeners.Delete(fmt.Sprintf("%p", listener))
}

func (sl *AgentList) ResetStartTime() {
	sl.l.Info().Msg("Resetting start time")
	sl.uptimeDelegate.ResetStartTime()
	err := sl.ml.UpdateNode(30 * time.Second)
	if err != nil {
		sl.l.Error().Msgf("Failed to update node: %v", err.Error())
		return
	}
	sl.l.Info().Msg("Start time reset")
}

func listAllInterfacesIps() ([]string, error) {
	var ips []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			switch v := addr.(type) {
			case *net.IPNet:
				ips = append(ips, v.IP.String())
			case *net.IPAddr:
				ips = append(ips, v.IP.String())
			}
		}
	}
	return ips, nil
}
