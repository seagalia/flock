package config

import (
	"errors"
	"fmt"

	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	sdkconfig "github.com/seagalia/flock/sdk/config"
	"go.yaml.in/yaml/v2"
)

// defaultListenPort leaves 18000 to a starling agent running on the same host and 18001 to its agent list
const defaultListenPort = 18002

// Config is the configuration of the provisioner. The log, controller, tls and server blocks are the ones of every
// agent.
type Config struct {
	Log              agentconfig.Log               `yaml:"log"`
	ControllerConfig *agentconfig.ControllerConfig `yaml:"controller"`
	TlsConfig        *agentconfig.TlsConfig        `yaml:"tls"`
	ServerConfig     *agentconfig.ServerConfig     `yaml:"server"`
	Acme             AcmeConfig                    `yaml:"acme"`
	Docker           DockerConfig                  `yaml:"docker"`
}

func (c *Config) UnmarshalYAML(unmarshal func(any) error) error {
	// before the real read, a strict one would only say the key is unknown
	err := refuseIdentityKeys(unmarshal)
	if err != nil {
		return err
	}
	// a server block without listen_port keeps this port
	c.ServerConfig = &agentconfig.ServerConfig{ListenPort: defaultListenPort}
	type plain Config
	err = unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.ControllerConfig == nil {
		return fmt.Errorf("controller config is required")
	}
	if c.TlsConfig == nil {
		return fmt.Errorf("tls config is required")
	}
	// a read server block always has a host, its own checks refuse an empty one, so an empty host means no block
	isServerBlockMissing := c.ServerConfig == nil || c.ServerConfig.ListenHost == ""
	if isServerBlockMissing {
		c.ServerConfig, err = agentconfig.DefaultServerConfig()
		if err != nil {
			return err
		}
		c.ServerConfig.ListenPort = defaultListenPort
	}
	isEdsOnServerPort := c.Docker.Enabled && c.Docker.Eds.ListenPort == c.ServerConfig.ListenPort
	if isEdsOnServerPort {
		return fmt.Errorf("docker eds.listen_port %d is the port of the server", c.Docker.Eds.ListenPort)
	}
	return nil
}

// refuseIdentityKeys fails on the keys that set the identity of the provisioner, which its certificate and the
// controller give. Ignoring them would run it under another identity than the one its config asks for.
func refuseIdentityKeys(unmarshal func(any) error) error {
	// the inline map takes every other key, so a strict read accepts them here
	var keys struct {
		Values     map[string]any `yaml:",inline"`
		Controller map[string]any `yaml:"controller"`
	}
	err := unmarshal(&keys)
	if err != nil {
		return err
	}
	if _, hasNodeName := keys.Values["node_name"]; hasNodeName {
		return errors.New("node_name is no longer read, the name of the provisioner is the common name of its certificate (tls.cert_file)")
	}
	if _, hasAgentUuid := keys.Controller["agent_uuid"]; hasAgentUuid {
		return errors.New("controller.agent_uuid is no longer read, the controller gives the uuid of the provisioner when it connects")
	}
	return nil
}

// ReadConfig reads the config file merged with the yaml of ConfigPatchEnv
func ReadConfig(configPath string) (*Config, error) {
	return readConfig(configPath, yaml.Unmarshal)
}

// ReadConfigStrict reads the config like ReadConfig but also refuses the keys the config doesn't know: ReadConfig
// ignores them, so a misspelled key silently keeps its default.
func ReadConfigStrict(configPath string) (*Config, error) {
	return readConfig(configPath, yaml.UnmarshalStrict)
}

func readConfig(configPath string, unmarshal func([]byte, any) error) (*Config, error) {
	b, err := sdkconfig.ReadFile(configPath, ConfigPatchEnv)
	if err != nil {
		return nil, err
	}
	cnf := &Config{}
	err = unmarshal(b, cnf)
	if err != nil {
		return nil, err
	}
	return cnf, nil
}
