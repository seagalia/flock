package overview

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/seagalia/flock/sdk/clients"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/backend/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/core/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/instance/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vip/v1"
	"github.com/seagalia/flock/sdk/flock/api/config/vs/v1"
	svccore "github.com/seagalia/flock/sdk/flock/services/core/v1"
	svcinstance "github.com/seagalia/flock/sdk/flock/services/instance/v1"
	svclookup "github.com/seagalia/flock/sdk/flock/services/lookup/v1"
	svcvip "github.com/seagalia/flock/sdk/flock/services/vip/v1"
	svcvs "github.com/seagalia/flock/sdk/flock/services/vs/v1"
	"github.com/seagalia/flock/sdk/helpers"
)

const pageLimit = 100

// VsInfo is a virtual server of the instance with the VIPs announcing it
type VsInfo struct {
	vs   *vs.VirtualServer
	vips []*vip.VIP
}

// Addresses gives one ip:port per VIP announcing the virtual server
func (v *VsInfo) Addresses() []string {
	addresses := make([]string, 0, len(v.vips))
	for _, aVip := range v.vips {
		ip := aVip.GetIp()
		if strings.Contains(ip, ":") {
			ip = "[" + ip + "]"
		}
		addresses = append(addresses, fmt.Sprintf("%s:%d", ip, v.vs.GetPort()))
	}
	return addresses
}

func (v *VsInfo) GetUUID() string {
	if v == nil {
		return ""
	}
	return v.vs.GetUUID()
}

func (v *VsInfo) GetName() string {
	if v == nil {
		return ""
	}
	return v.vs.GetName()
}

func (v *VsInfo) GetBackends() []string {
	if v == nil {
		return []string{}
	}
	return v.vs.GetBackendUuids()
}

func (v *VsInfo) GetType() string {
	switch v.vs.GetRouteConfig().(type) {
	case *vs.VirtualServer_TcpRouteConfig:
		return "TCP"
	case *vs.VirtualServer_HttpRoutesConfig:
		return "HTTP"
	case *vs.VirtualServer_TlsTcpRoutesConfig:
		return "TLS+TCP"
	case *vs.VirtualServer_UdpRouteConfig:
		return "UDP"
	}
	return "Unknown"
}

// BackendInfo is a backend read from the controller, err is set when it could not be read
type BackendInfo struct {
	backend *backend.Backend
	err     error
}

func (c *BackendInfo) GetUUID() string {
	if c == nil {
		return ""
	}
	return c.backend.GetUUID()
}

func (c *BackendInfo) GetName() string {
	if c == nil {
		return ""
	}
	return c.backend.GetName()
}

func (c *BackendInfo) Type() string {
	switch c.backend.GetEndpoint().(type) {
	case *backend.Backend_Dns:
		return "DNS"
	case *backend.Backend_IpAddrGroup:
		return "Static"
	case *backend.Backend_Eds:
		return "EDS"
	}
	return "Unknown"
}

// EndpointConfig describes where the endpoints come from, static addresses are listed with the endpoints instead
func (c *BackendInfo) EndpointConfig() string {
	switch c.backend.GetEndpoint().(type) {
	case *backend.Backend_Dns:
		dns := c.backend.GetDns()
		return fmt.Sprintf("dns %s:%d %s", dns.GetHost(), dns.GetPort(), dns.GetLookupFamily())
	case *backend.Backend_IpAddrGroup:
		return "static"
	case *backend.Backend_Eds:
		eds := c.backend.GetEds()
		if eds.GetServiceName() == "" {
			return "eds " + eds.GetUrl().Format()
		}
		return "eds " + eds.GetUrl().Format() + " service " + eds.GetServiceName()
	}
	return "unknown endpoints"
}

// StaticAddresses gives the configured addresses of a static backend, nil for the other types
func (c *BackendInfo) StaticAddresses() []*core.IPAddr {
	return c.backend.GetIpAddrGroup().GetIpAddresses()
}

func (c *BackendInfo) Policy() string {
	switch c.backend.GetLbPolicy() {
	case backend.Backend_ROUND_ROBIN:
		return "Round Robin"
	case backend.Backend_WEIGHTED_ROUND_ROBIN:
		return "Weighted Round Robin"
	case backend.Backend_WEIGHTED_LEAST_REQUEST:
		return "Weighted Least Request"
	case backend.Backend_LEAST_REQUEST:
		return "Least Request"
	case backend.Backend_RING_HASH:
		return "Ring Hash"
	case backend.Backend_MAGLEV:
		return "Maglev"
	}
	return "Unknown"
}

func (c *BackendInfo) HealthcheckType() string {
	switch c.backend.GetHealthCheck().GetHealthChecker().(type) {
	case *core.HealthCheck_HttpHealthCheck:
		return "HTTP"
	case *core.HealthCheck_TcpHealthCheck:
		return "TCP"
	case *core.HealthCheck_UdpHealthCheck:
		return "UDP"
	case *core.HealthCheck_GrpcHealthCheck:
		return "GRPC"
	case *core.HealthCheck_NoHealthCheck:
		return "None"
	case *core.HealthCheck_PluginHealthCheck:
		return "Plugin " + c.backend.GetHealthCheck().GetPluginHealthCheck().GetName()
	}
	return "Unknown"
}

func (c *BackendInfo) ProxyProtocol() string {
	if !c.backend.GetEnableProxyProtocol() {
		return "off"
	}
	return c.backend.GetProxyProtocolVersion().String()
}

// instanceData is everything shown for an instance except the backends, which are read when a virtual server is expanded
type instanceData struct {
	vss    []*VsInfo
	agents []*svclookup.GetAgentStatusResponse
	// endpoints by backend uuid, every agent reports its own view of the endpoints
	endpoints map[string][]*backend.EndpointStat
}

type instancesLoadedMsg struct {
	instances []*instance.Instance
	err       error
}

type instanceLoadedMsg struct {
	instanceUuid string
	data         *instanceData
	err          error
}

type backendsLoadedMsg struct {
	backends []*BackendInfo
}

func loadInstances(ctx context.Context, client *clients.Client) tea.Cmd {
	return func() tea.Msg {
		var instances []*instance.Instance
		for offset := 0; ; offset += pageLimit {
			resp, err := client.Instance().ListInstance(ctx, &svcinstance.ListInstanceRequest{
				Limit:  pageLimit,
				Offset: int32(offset),
			})
			if err != nil {
				return instancesLoadedMsg{err: err}
			}
			if len(resp.GetInstances()) == 0 {
				return instancesLoadedMsg{instances: instances}
			}
			instances = append(instances, resp.GetInstances()...)
		}
	}
}

func loadInstance(ctx context.Context, client *clients.Client, inst *instance.Instance) tea.Cmd {
	return func() tea.Msg {
		data, err := readInstanceData(ctx, client, inst)
		// a load cancelled by a reload has nothing to show, the next one answers
		if ctx.Err() != nil {
			return nil
		}
		return instanceLoadedMsg{instanceUuid: inst.GetUUID(), data: data, err: err}
	}
}

func loadBackends(ctx context.Context, client *clients.Client, backendUuids []string) tea.Cmd {
	if len(backendUuids) == 0 {
		return nil
	}
	return func() tea.Msg {
		backends := make([]*BackendInfo, 0, len(backendUuids))
		for _, backendUuid := range backendUuids {
			resp, err := client.Backend().ReadBackend(ctx, &svccore.UuidRequest{UUID: backendUuid})
			if err != nil {
				// a placeholder keeps the uuid so the error is shown under the right virtual server
				backends = append(backends, &BackendInfo{backend: &backend.Backend{UUID: backendUuid}, err: err})
				continue
			}
			backends = append(backends, &BackendInfo{backend: resp.GetBackend()})
		}
		// reads cancelled by a reload would only leave placeholders with an error
		if ctx.Err() != nil {
			return backendsLoadedMsg{}
		}
		return backendsLoadedMsg{backends: backends}
	}
}

func readInstanceData(ctx context.Context, client *clients.Client, inst *instance.Instance) (*instanceData, error) {
	vips, err := readVips(ctx, client, inst)
	if err != nil {
		return nil, err
	}
	vss, err := readVss(ctx, client, vips)
	if err != nil {
		return nil, err
	}
	agents, err := readAgentStatus(ctx, client, inst)
	if err != nil {
		return nil, err
	}
	endpoints, err := readEndpoints(ctx, client, inst)
	if err != nil {
		return nil, err
	}
	return &instanceData{vss: vss, agents: agents, endpoints: endpoints}, nil
}

func readVips(ctx context.Context, client *clients.Client, inst *instance.Instance) ([]*vip.VIP, error) {
	filters := []*svccore.ListFilter{{Key: helpers.TypeUrl[*instance.Instance](), Value: inst.GetUUID()}}
	var vips []*vip.VIP
	for offset := 0; ; offset += pageLimit {
		resp, err := client.Vip().ListVIP(ctx, &svcvip.ListVIPRequest{
			Limit:   pageLimit,
			Offset:  int32(offset),
			Filters: filters,
		})
		if err != nil {
			return nil, err
		}
		if len(resp.GetVip()) == 0 {
			return vips, nil
		}
		vips = append(vips, resp.GetVip()...)
	}
}

func readVss(ctx context.Context, client *clients.Client, vips []*vip.VIP) ([]*VsInfo, error) {
	// with no filter the list would give every virtual server, not the ones of this instance
	if len(vips) == 0 {
		return nil, nil
	}
	filters := make([]*svccore.ListFilter, 0, len(vips))
	vipsByVs := make(map[string][]*vip.VIP)
	for _, aVip := range vips {
		filters = append(filters, &svccore.ListFilter{Key: helpers.TypeUrl[*vip.VIP](), Value: aVip.GetUUID()})
		for _, vsUuid := range aVip.GetVirtualServersUuid() {
			vipsByVs[vsUuid] = append(vipsByVs[vsUuid], aVip)
		}
	}
	var vss []*VsInfo
	for offset := 0; ; offset += pageLimit {
		resp, err := client.VirtualServer().ListVS(ctx, &svcvs.ListVSRequest{
			Limit:   pageLimit,
			Offset:  int32(offset),
			Filters: filters,
		})
		if err != nil {
			return nil, err
		}
		if len(resp.GetVs()) == 0 {
			return vss, nil
		}
		for _, aVs := range resp.GetVs() {
			vss = append(vss, &VsInfo{vs: aVs, vips: vipsByVs[aVs.GetUUID()]})
		}
	}
}

// readAgentStatus keeps the load balancer agents which show a status, provisioners are not counted as agents
func readAgentStatus(ctx context.Context, client *clients.Client, inst *instance.Instance) ([]*svclookup.GetAgentStatusResponse, error) {
	respAgents, err := client.Lookup().ListAgent(ctx, &svclookup.ListAgentRequest{InstanceUuid: inst.GetUUID()})
	if err != nil {
		return nil, err
	}
	agentsByUuid := make(map[string]*agent.Agent)
	for _, anAgent := range respAgents.GetAgents() {
		agentsByUuid[anAgent.GetUUID()] = anAgent
	}
	resp, err := client.Lookup().ListAgentStatus(ctx, &svclookup.ListAgentStatusRequest{InstanceUuid: inst.GetUUID()})
	if err != nil {
		return nil, err
	}
	statuses := make([]*svclookup.GetAgentStatusResponse, 0)
	for _, agentStatus := range resp.GetAgentStatus() {
		anAgent := agentsByUuid[agentStatus.GetAgentUuid()]
		if anAgent == nil {
			continue
		}
		isProvisioner := anAgent.GetMode() == agent.Agent_PROVISIONER
		showsStatus := anAgent.GetFeatures().GetShowStatus()
		if isProvisioner || !showsStatus {
			continue
		}
		statuses = append(statuses, agentStatus)
	}
	return statuses, nil
}

func readEndpoints(ctx context.Context, client *clients.Client, inst *instance.Instance) (map[string][]*backend.EndpointStat, error) {
	resp, err := client.Lookup().ListStatus(ctx, &svclookup.ListStatusRequest{
		InstanceUuid: inst.GetUUID(),
		TypeUrl:      helpers.TypeUrl[*backend.BackendEndpointStats](),
	})
	if err != nil {
		return nil, err
	}
	endpointsByBackend := make(map[string][]*backend.EndpointStat)
	for _, status := range resp.GetStatus() {
		stats := &backend.BackendEndpointStats{}
		if status.GetPreviousResource().UnmarshalTo(stats) != nil {
			continue
		}
		backendUuid := stats.GetBackendUuid()
		endpointsByBackend[backendUuid] = append(endpointsByBackend[backendUuid], stats.GetEndpointStats()...)
	}
	return endpointsByBackend, nil
}
