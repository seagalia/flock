package config

import (
	"fmt"

	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	sdkconfig "github.com/seagalia/flock/sdk/config"
	"go.yaml.in/yaml/v2"
)

// Config is the configuration of the F5 agent. The log, controller, tls and server blocks are the ones of every agent.
type Config struct {
	Log              agentconfig.Log               `yaml:"log"`
	ControllerConfig *agentconfig.ControllerConfig `yaml:"controller"`
	TlsConfig        *agentconfig.TlsConfig        `yaml:"tls"`
	ServerConfig     *agentconfig.ServerConfig     `yaml:"server"`
	Bigip            *BigipConfig                  `yaml:"bigip"`
}

func (c *Config) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Config
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.ControllerConfig == nil {
		return fmt.Errorf("controller config is required")
	}
	if c.TlsConfig == nil {
		return fmt.Errorf("tls config is required")
	}
	if c.Bigip == nil {
		return fmt.Errorf("bigip config is required")
	}
	if c.ServerConfig == nil {
		c.ServerConfig, err = agentconfig.DefaultServerConfig()
		if err != nil {
			return err
		}
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
