package config

import (
	"fmt"
	"time"

	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
	sdkconfig "github.com/seagalia/flock/sdk/config"
	"github.com/seagalia/flock/sdk/models"
	"go.yaml.in/yaml/v2"
)

type Config struct {
	Log                   agentconfig.Log               `yaml:"log"`
	ControllerConfig      *agentconfig.ControllerConfig `yaml:"controller"`
	AdvertisementConfig   *AdvertisementConfig          `yaml:"advertisement"`
	TlsConfig             *agentconfig.TlsConfig        `yaml:"tls"`
	ServerConfig          *agentconfig.ServerConfig     `yaml:"server"`
	EnvoyConfig           *EnvoyConfig                  `yaml:"envoy"`
	WafConfig             *WafConfig                    `yaml:"waf"`
	WhenGarbageAfterStart *models.Duration              `yaml:"when_garbage_after_start"`
	MetricsConfig         *MetricsConfig                `yaml:"metrics"`
	DevMode               bool                          `yaml:"dev_mode"`
	DisableL3Log          bool                          `yaml:"disable_l3_log"`
	Plugin                *Plugin                       `yaml:"plugin"`
	System                *SystemConfig                 `yaml:"system"`
}

func (u *Config) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Config
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if u.ControllerConfig == nil {
		return fmt.Errorf("controller config is required")
	}
	if u.AdvertisementConfig == nil {
		u.AdvertisementConfig = &AdvertisementConfig{}
		err = u.AdvertisementConfig.init()
		if err != nil {
			return err
		}
	}
	if u.System == nil {
		u.System = &SystemConfig{}
		err = u.System.init()
		if err != nil {
			return err
		}
	}
	if u.TlsConfig == nil {
		return fmt.Errorf("tls config is required")
	}
	if u.ServerConfig == nil {
		u.ServerConfig, err = agentconfig.DefaultServerConfig()
		if err != nil {
			return err
		}
	}
	if u.EnvoyConfig == nil {
		u.EnvoyConfig = &EnvoyConfig{
			EnvoyExec: &EnvoyExec{
				BinPath: "envoy",
			},
			MaxConnections: DefaultEnvoyMaxConnections,
		}
	}
	if u.WafConfig == nil {
		u.WafConfig = &WafConfig{
			WafEmbedded: true,
		}
	}
	if u.Plugin == nil {
		u.Plugin = &Plugin{}
	}
	if u.WhenGarbageAfterStart == nil {
		dur := 10 * time.Minute
		u.WhenGarbageAfterStart = (*models.Duration)(&dur)
	}

	if u.MetricsConfig == nil {
		u.MetricsConfig = &MetricsConfig{}
	}
	err = u.MetricsConfig.init()
	if err != nil {
		return err
	}

	return nil
}

// ReadConfig reads the config file merged with the yaml of ConfigPatchEnv
func ReadConfig(configPath string) (*Config, error) {
	return readConfig(configPath, yaml.Unmarshal)
}

// ReadConfigStrict reads the config like ReadConfig but also refuses the keys the config doesn't know:
// ReadConfig ignores them, so a misspelled key silently keeps its default.
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
