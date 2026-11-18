package devenv

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	starlingconfig "github.com/seagalia/flock/agent/starling/config"
	controllerconfig "github.com/seagalia/flock/controller/config"
	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	sdkconfig "github.com/seagalia/flock/sdk/config"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"go.yaml.in/yaml/v2"
)

// What a dev session creates, users see them in the banner and in the cli.
const (
	controllerPort = "8443"
	// dashboardAddr serves the dashboard over plain http, a browser refuses the self-signed certificate of the controller
	dashboardAddr   = "127.0.0.1:8480"
	agentListenPort = 18000
	agentHost       = "127.0.0.1:18000"
	// agentName is the CN of the agent certificate, controller gives the agent the uuid of the agent declared so
	agentName = "local"
	agentUuid = "063cd244-c554-499c-998d-8108d4c8647f"

	provisionerName       = "local-provisioner"
	provisionerUuid       = "10876b6f-87f9-439c-b3a3-62f0368694cb"
	provisionerListenHost = "127.0.0.1"
	provisionerListenPort = 18002
	// provisionerAdminGroup is the group controller gives the calls made with the provisioner certificate, it needs
	// admin to write the certificates and secrets it provisions
	provisionerAdminGroup = "agent-" + provisionerName

	adminUser             = "admin"
	loopbackAdminPassword = "admin"

	zoneName         = "local"
	resourceTypeName = "local"
	instanceName     = "local"
	vipName          = "local"
	// a wildcard, which envoy and IPVS both serve on every address of the machine
	vipIp = "0.0.0.0"

	jwtIssuer   = "flock-dev"
	jwtAudience = "flock"

	// a local dev store: a fixed secret is enough
	storeSecret = "flock-dev"
)

// controllerSettings is what changes between a loopback session and an exposed one.
type controllerSettings struct {
	StorePath, LogLevel, ListenAddr, AdminPassword string
}

// agentSettings is what changes between the unprivileged agent (dev mode) and the privileged one.
type agentSettings struct {
	InstanceUuid, EnvoyPath, LogLevel, ControllerAddr string
	Privileged                                        bool
	// SnatIfaces are the interfaces IPVS traffic leaves by, the agent source-NATs on them so that the backends answer
	// through this machine. Empty when nothing is to be NATed.
	SnatIfaces []string
}

var provisionerHost = net.JoinHostPort(provisionerListenHost, strconv.Itoa(provisionerListenPort))

// renderControllerConfig gives the controller yaml config, the store is kept in s.StorePath across sessions.
func renderControllerConfig(certs *pki, s controllerSettings) ([]byte, error) {
	localAgent := &controllerconfig.AgentInstantiator{
		UUID: agentUuid,
		Name: agentName,
		Host: agentHost,
	}
	localAgent.Features.ShowLogs = true
	localAgent.Features.ShowMetrics = true
	localAgent.Features.ShowStatus = true

	provisionerMode := controllerconfig.AgentMode(agent.Agent_PROVISIONER.String())
	localProvisioner := &controllerconfig.AgentInstantiator{
		UUID: provisionerUuid,
		Name: provisionerName,
		Host: provisionerHost,
		Type: "provisioner",
		Mode: &provisionerMode,
	}
	// the provisioner shares the logging of the process with the agent, whose setup wins: its logs stream stays empty
	localProvisioner.Features.ShowLogs = false
	localProvisioner.Features.ShowMetrics = true
	localProvisioner.Features.ShowStatus = true

	conf := &controllerconfig.Config{
		Log: controllerconfig.Log{Level: s.LogLevel},
		Server: &controllerconfig.Server{
			ListenAddr:       s.ListenAddr,
			LoopbackHttpAddr: dashboardAddr,
			Tls: &controllerconfig.ServerTls{
				CertFile: certs.ControllerCertFile, KeyFile: certs.ControllerKeyFile, CaFile: certs.CaFile,
			},
			AdminConfig: controllerconfig.AdminConfig{Users: []string{adminUser}, Groups: []string{provisionerAdminGroup}},
			Dashboard:   controllerconfig.ServerDashboard{Enabled: true},
			Login: &controllerconfig.ServerLogin{
				Type: "basic",
				BasicAuth: []*controllerconfig.BasicAuthLogin{{
					Username: adminUser, Password: s.AdminPassword, Groups: []string{adminUser},
				}},
			},
			// the cli config does not keep the password: the token must outlive the session, it can't be renewed
			JwtMaker: &sdkconfig.JwtMaker{
				Alg:        sdkconfig.JwtAlg{AlgRaw: "RS256"},
				PrivateKey: certs.JwtPrivateKeyPem,
				ExpireIn:   sdkconfig.Duration(720 * time.Hour),
				Issuer:     jwtIssuer,
				Audience:   jwtAudience,
			},
		},
		Mtls: &controllerconfig.Mtls{
			CertFile: certs.ControllerAgentCertFile, KeyFile: certs.ControllerAgentKeyFile, CaFile: certs.CaFile,
			ServerName: agentServerName,
		},
		StoreManager: controllerconfig.StoreManagerConfig{
			SqlStorer: &controllerconfig.SqlStorer{
				DbType: "sqlite",
				// controller writes from several connections, they wait for each other instead of failing
				DSN:    s.StorePath + "?_pragma=busy_timeout(5000)",
				Secret: storeSecret,
			},
		},
		// the seeded vip changes its ip with the mode, which the ipam forbids, and the dev zone has no pools
		IPAM: &controllerconfig.IPAMConfig{Disabled: true},
		Instantiators: []*controllerconfig.Instantiator{{
			Name:          "local",
			Zones:         []string{zoneName},
			ResourceTypes: []*controllerconfig.ResourceType{{Name: resourceTypeName, Number: 1}},
			StaticInstantiator: &controllerconfig.StaticInstantiator{
				Agents: []*controllerconfig.AgentInstantiator{localAgent, localProvisioner},
			},
		}},
	}
	return yaml.Marshal(conf)
}

// renderAgentConfig gives the agent yaml config: dev mode unless privileged, envoy run from s.EnvoyPath, nothing
// advertised outside the loopback interface and s.SnatIfaces.
func renderAgentConfig(certs *pki, s agentSettings) ([]byte, error) {
	loopback, err := loopbackInterface()
	if err != nil {
		return nil, err
	}
	interfaces := append([]string{loopback}, s.SnatIfaces...)
	conf := &starlingconfig.Config{
		Log:     agentconfig.Log{Level: s.LogLevel},
		DevMode: !s.Privileged,
		// a container has no write access to the global sysctls (conntrack, net.core), the permissive mode only warns
		System: &starlingconfig.SystemConfig{SysctlMode: starlingconfig.SysctlModePermissive},
		ControllerConfig: &agentconfig.ControllerConfig{
			Addr: s.ControllerAddr, ServerName: "localhost", InstanceUuid: s.InstanceUuid,
		},
		TlsConfig: &agentconfig.TlsConfig{
			CertFile: certs.AgentCertFile, KeyFile: certs.AgentKeyFile, CaFile: certs.CaFile,
		},
		ServerConfig:        &agentconfig.ServerConfig{ListenHost: "127.0.0.1", ListenPort: agentListenPort},
		AdvertisementConfig: &starlingconfig.AdvertisementConfig{Interfaces: interfaces},
		EnvoyConfig:         &starlingconfig.EnvoyConfig{EnvoyExec: &starlingconfig.EnvoyExec{BinPath: s.EnvoyPath}},
		WafConfig:           &starlingconfig.WafConfig{WafEmbedded: true},
	}
	return yaml.Marshal(conf)
}

// loopbackInterface is lo on linux and lo0 on darwin, the agent checks its interfaces exist
func loopbackInterface() (string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			return iface.Name, nil
		}
	}
	return "", fmt.Errorf("no loopback interface found")
}

// writeControllerConfig writes the rendered config in path, for users to look at, and loads it
// like `controller serve` would: the yaml round trip applies the defaults of the config types.
func writeControllerConfig(certs *pki, s controllerSettings, path string) (*controllerconfig.Config, error) {
	rendered, err := renderControllerConfig(certs, s)
	if err != nil {
		return nil, err
	}
	err = os.WriteFile(path, rendered, 0600)
	if err != nil {
		return nil, err
	}
	return controllerconfig.ReadConfig(path)
}

func writeAgentConfig(certs *pki, s agentSettings, path string) (*starlingconfig.Config, error) {
	rendered, err := renderAgentConfig(certs, s)
	if err != nil {
		return nil, err
	}
	err = os.WriteFile(path, rendered, 0600)
	if err != nil {
		return nil, err
	}
	return starlingconfig.ReadConfig(path)
}

// internalControllerAddr is where the apps of the process reach the controller: through the loopback when it listens
// on every address, on the address itself otherwise (a specific loopback address included).
func internalControllerAddr(listenHost string) string {
	ip := net.ParseIP(listenHost)
	if ip != nil && ip.IsUnspecified() {
		return net.JoinHostPort("127.0.0.1", controllerPort)
	}
	return net.JoinHostPort(listenHost, controllerPort)
}

func isExposed(listenHost string) bool {
	if listenHost == "localhost" {
		return false
	}
	ip := net.ParseIP(listenHost)
	return ip == nil || !ip.IsLoopback()
}
