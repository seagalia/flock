//go:build e2e

package env

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/seagalia/flock/sdk/models"
	"github.com/testcontainers/testcontainers-go"
	"go.yaml.in/yaml/v2"

	"github.com/seagalia/flock/agent/starling/config"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
)

const (
	agentConfigPath = "/etc/flock/agent.yml"
	agentTlsDir     = "/etc/flock/tls"
	envoyAdmin      = "http://127.0.0.1:19000"
	agentServer     = "https://127.0.0.1:18000"
)

type AgentOptions struct {
	// Name is the hostname of the container and the CN of its certificate, so the name of the agent
	Name     string
	IP       string
	Instance string
	// ControllerAddr is a real controller the agent dials instead of the fake one, as host:port. That controller
	// declares the agent, the fake one is told here.
	ControllerAddr string
	Configure      func(conf *config.Config)
}

// Agent is a privileged container running flock-agent and envoy
type Agent struct {
	Container testcontainers.Container
	opts      AgentOptions
}

type IpvsService struct {
	Proto     string
	Addr      string
	Scheduler string
	Dests     []IpvsDest
}

type IpvsDest struct {
	Addr   string
	Fwd    string
	Weight int
}

func (e *Env) StartAgent(ctx context.Context, o AgentOptions) (*Agent, error) {
	image, err := e.image(ctx, "agent")
	if err != nil {
		return nil, err
	}
	certPem, keyPem, err := e.Certs.AgentCert(o.Name)
	if err != nil {
		return nil, fmt.Errorf("certificate of agent %s: %w", o.Name, err)
	}
	controllerAddr := o.ControllerAddr
	if controllerAddr == "" {
		controllerAddr = fmt.Sprintf("host.testcontainers.internal:%d", e.ControllerPort)
		e.Controller.DeclareAgent(o.Name, AgentUuid(o.Name))
	}
	garbageAfter := models.Duration(time.Hour)
	conf := &config.Config{
		WhenGarbageAfterStart: &garbageAfter,
		Log:                   agentconfig.Log{Level: "debug"},
		ControllerConfig: &agentconfig.ControllerConfig{
			Addr:         controllerAddr,
			ServerName:   ControllerServerName,
			InstanceUuid: o.Instance,
		},
		TlsConfig: &agentconfig.TlsConfig{
			CertFile: agentTlsDir + "/cert.pem",
			KeyFile:  agentTlsDir + "/key.pem",
			CaFile:   agentTlsDir + "/ca.pem",
		},
		ServerConfig: &agentconfig.ServerConfig{ListenHost: o.IP, ListenPort: 18000},
		AdvertisementConfig: &config.AdvertisementConfig{
			Interfaces: []string{"eth0"},
			L2Config:   &config.L2AdvConfig{},
		},
		EnvoyConfig: &config.EnvoyConfig{EnvoyExec: &config.EnvoyExec{BinPath: "/usr/local/bin/envoy"}},
		// a container refuses some host global sysctls
		System: &config.SystemConfig{SysctlMode: config.SysctlModePermissive},
	}
	if o.Configure != nil {
		o.Configure(conf)
	}
	confYaml, err := yaml.Marshal(conf)
	if err != nil {
		return nil, fmt.Errorf("render agent config: %w", err)
	}

	c, err := e.start(ctx, o.IP, testcontainers.ContainerRequest{
		Image:           image,
		HostAccessPorts: []int{e.ControllerPort},
		ConfigModifier:  withHostname(o.Name),
		HostConfigModifier: func(hc *container.HostConfig) {
			hc.Privileged = true
			hc.Binds = append(hc.Binds, "/lib/modules:/lib/modules:ro")
		},
		Files: []testcontainers.ContainerFile{
			fileFromBytes(confYaml, agentConfigPath, 0o644),
			fileFromBytes(certPem, agentTlsDir+"/cert.pem", 0o644),
			fileFromBytes(keyPem, agentTlsDir+"/key.pem", 0o600),
			fileFromBytes(e.Certs.CAPem, agentTlsDir+"/ca.pem", 0o644),
		},
	})
	if err != nil {
		return nil, err
	}
	return &Agent{Container: c, opts: o}, nil
}

func (m *Agent) IP() string {
	return m.opts.IP
}

func (m *Agent) Exec(ctx context.Context, cmd ...string) (string, error) {
	return execIn(ctx, m.Container, cmd...)
}

func (m *Agent) Logs(ctx context.Context) string {
	return containerLogs(ctx, m.Container)
}

func (m *Agent) MAC(ctx context.Context) string {
	out, _ := m.Exec(ctx, "cat", "/sys/class/net/eth0/address")
	return strings.TrimSpace(out)
}

// LocalRoutes gives the vip cidrs local on flock0, as addresses or AnyIP routes
func (m *Agent) LocalRoutes(ctx context.Context) []string {
	out, err := m.Exec(ctx, "ip", "-4", "route", "show", "table", "local", "dev", "flock0")
	if err != nil {
		return nil
	}
	routes := make([]string, 0)
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		isLocalRoute := len(fields) >= 2 && fields[0] == "local"
		if !isLocalRoute || strings.HasPrefix(fields[1], "127.") {
			continue
		}
		dst := fields[1]
		if !strings.Contains(dst, "/") {
			dst += "/32"
		}
		routes = append(routes, dst)
	}
	return routes
}

// FlockNftables gives the lines of the agent table, none when the table doesn't exist
func (m *Agent) FlockNftables(ctx context.Context) []string {
	out, err := m.Exec(ctx, "nft", "list", "table", "inet", "flock")
	if err != nil {
		return nil
	}
	lines := make([]string, 0)
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func (m *Agent) Ipvs(ctx context.Context) []IpvsService {
	out, err := m.Exec(ctx, "ipvsadm", "-Ln")
	if err != nil {
		return nil
	}
	return parseIpvsadm(out)
}

func parseIpvsadm(out string) []IpvsService {
	services := make([]IpvsService, 0)
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		switch {
		case (fields[0] == "TCP" || fields[0] == "UDP") && len(fields) >= 3:
			services = append(services, IpvsService{Proto: fields[0], Addr: fields[1], Scheduler: fields[2], Dests: []IpvsDest{}})
		case fields[0] == "->" && len(fields) >= 4 && fields[1] != "RemoteAddress:Port" && len(services) > 0:
			weight := 0
			_, _ = fmt.Sscanf(fields[3], "%d", &weight)
			last := &services[len(services)-1]
			last.Dests = append(last.Dests, IpvsDest{Addr: fields[1], Fwd: fields[2], Weight: weight})
		}
	}
	return services
}

func (m *Agent) EnvoyListeners(ctx context.Context) []string {
	out, err := m.Exec(ctx, "curl", "-sf", envoyAdmin+"/listeners?format=json")
	if err != nil {
		return nil
	}
	var resp struct {
		ListenerStatuses []struct {
			Name string `json:"name"`
		} `json:"listener_statuses"`
	}
	if json.Unmarshal([]byte(out), &resp) != nil {
		return nil
	}
	names := make([]string, 0)
	for _, l := range resp.ListenerStatuses {
		names = append(names, l.Name)
	}
	return names
}

// EnvoyClusterHosts gives the hosts of the envoy cluster as ip:port
func (m *Agent) EnvoyClusterHosts(ctx context.Context, backend string) []string {
	out, err := m.Exec(ctx, "curl", "-sf", envoyAdmin+"/clusters?format=json")
	if err != nil {
		return nil
	}
	var resp struct {
		ClusterStatuses []struct {
			Name         string `json:"name"`
			HostStatuses []struct {
				Address struct {
					SocketAddress struct {
						Address   string `json:"address"`
						PortValue int    `json:"port_value"`
					} `json:"socket_address"`
				} `json:"address"`
			} `json:"host_statuses"`
		} `json:"cluster_statuses"`
	}
	if json.Unmarshal([]byte(out), &resp) != nil {
		return nil
	}
	hosts := make([]string, 0)
	for _, c := range resp.ClusterStatuses {
		if c.Name != backend {
			continue
		}
		for _, h := range c.HostStatuses {
			hosts = append(hosts, fmt.Sprintf("%s:%d", h.Address.SocketAddress.Address, h.Address.SocketAddress.PortValue))
		}
	}
	return hosts
}

func (m *Agent) Metrics(ctx context.Context) string {
	out, _ := m.Exec(ctx, "curl", "-sk", agentServer+"/metrics")
	return out
}

// AnnouncedVips reads the agent companion events stream
func (m *Agent) AnnouncedVips(ctx context.Context) []string {
	out, _ := m.Exec(ctx, "sh", "-c",
		"curl -sk -N --max-time 3 '"+agentServer+"/companion/v1/events?stream=announced_vips' | grep -m1 '^data:'")
	data := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "data:"))
	vips := make([]string, 0)
	_ = json.Unmarshal([]byte(data), &vips)
	return vips
}

func (m *Agent) Signal(ctx context.Context, sig string) error {
	_, err := m.Exec(ctx, "pkill", "-"+sig, "-x", "flock")
	return err
}

// Pid is empty when the agent is not running
func (m *Agent) Pid(ctx context.Context) string {
	out, _ := m.Exec(ctx, "pgrep", "-x", "flock")
	return strings.TrimSpace(out)
}

// RestartProcess relies on the container entrypoint to start the agent again, so the network namespace is kept
func (m *Agent) RestartProcess(ctx context.Context) error {
	return m.Signal(ctx, "TERM")
}

func (m *Agent) StopRestarting(ctx context.Context) error {
	_, err := m.Exec(ctx, "touch", "/tmp/stop")
	return err
}

// Freeze stops the agent process without closing anything, like a node that hangs: only a peer
// watching it (bfd, hold time) sees it is gone
func (m *Agent) Freeze(ctx context.Context) error {
	_, err := m.Exec(ctx, "pkill", "-STOP", "-x", "flock")
	return err
}

func (m *Agent) Resume(ctx context.Context) error {
	_, err := m.Exec(ctx, "pkill", "-CONT", "-x", "flock")
	return err
}

func (m *Agent) Stop(ctx context.Context) error {
	return m.Container.Stop(ctx, nil)
}

func (m *Agent) Start(ctx context.Context) error {
	return m.Container.Start(ctx)
}

func (m *Agent) Dump(ctx context.Context) map[string]string {
	logs := m.Logs(ctx)
	dump := map[string]string{"agent logs": tail(agentOnlyLogs(logs), 400)}
	if f, err := os.CreateTemp("", "flock-e2e-"+m.opts.Name+"-*.log"); err == nil {
		_, _ = f.WriteString(logs)
		_ = f.Close()
		dump["agent full logs file"] = f.Name()
	}
	for name, cmd := range map[string][]string{
		"nft ruleset":    {"nft", "list", "ruleset"},
		"ipvsadm":        {"ipvsadm", "-Ln"},
		"local routes":   {"ip", "route", "show", "table", "local"},
		"flock0":         {"ip", "addr", "show", "dev", "flock0"},
		"envoy config":   {"curl", "-s", envoyAdmin + "/config_dump"},
		"envoy clusters": {"curl", "-s", envoyAdmin + "/clusters"},
	} {
		out, err := m.Exec(ctx, cmd...)
		if err != nil {
			out += "\nerror: " + err.Error()
		}
		dump[name] = out
	}
	return dump
}

// agentOnlyLogs keeps envoy warnings and errors only
func agentOnlyLogs(logs string) string {
	kept := make([]string, 0)
	for line := range strings.SplitSeq(logs, "\n") {
		// the agent container has no terminal: colorless text with WRN/ERR/FTL level labels
		isEnvoy := strings.Contains(line, "component=envoy")
		isProblem := strings.Contains(line, " WRN ") || strings.Contains(line, " ERR ") || strings.Contains(line, " FTL ")
		if isEnvoy && !isProblem {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func tail(content string, lines int) string {
	all := strings.Split(content, "\n")
	if len(all) <= lines {
		return content
	}
	return strings.Join(all[len(all)-lines:], "\n")
}

// EnvoyClusters includes static clusters
func (m *Agent) EnvoyClusters(ctx context.Context) []string {
	out, err := m.Exec(ctx, "curl", "-sf", envoyAdmin+"/clusters?format=json")
	if err != nil {
		return nil
	}
	var resp struct {
		ClusterStatuses []struct {
			Name string `json:"name"`
		} `json:"cluster_statuses"`
	}
	if json.Unmarshal([]byte(out), &resp) != nil {
		return nil
	}
	names := make([]string, 0)
	for _, c := range resp.ClusterStatuses {
		names = append(names, c.Name)
	}
	return names
}
