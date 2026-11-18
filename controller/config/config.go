package config

import (
	"fmt"
	"os"

	sdkconfig "github.com/seagalia/flock/sdk/config"
	"github.com/seagalia/flock/sdk/logging"
	"go.yaml.in/yaml/v2"
)

type Config struct {
	Server        *Server            `yaml:"server"`
	Log           Log                `yaml:"log"`
	Mtls          *Mtls              `yaml:"mtls"`
	StoreManager  StoreManagerConfig `yaml:"store"`
	Instantiators []*Instantiator    `yaml:"instantiators"`
	Multi         *Multi             `yaml:"multi"`
	IPAM          *IPAMConfig        `yaml:"ipam"`
}

type Mtls struct {
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	CaFile     string `yaml:"ca_file"`
	ServerName string `yaml:"server_name"`
}

func (u *Mtls) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Mtls
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if u.CertFile == "" {
		return fmt.Errorf("mtls_cert_file config: cert file is empty")
	}
	if u.KeyFile == "" {
		return fmt.Errorf("mtls_cert_file config: key file is empty")
	}

	_, err = os.Stat(u.CertFile)
	if err != nil {
		return fmt.Errorf("mtls config: cert file not found: %w", err)
	}

	_, err = os.Stat(u.KeyFile)
	if err != nil {
		return fmt.Errorf("mtls config: key file not found: %w", err)
	}

	if u.CaFile != "" {
		_, err = os.Stat(u.CaFile)
		if err != nil {
			return fmt.Errorf("mtls config: ca file not found: %w", err)
		}
	}
	if u.ServerName == "" {
		u.ServerName = "flock_agent"
	}
	return nil
}

type Log struct {
	Level            string   `yaml:"level"`
	NoColor          bool     `yaml:"no_color"`
	ForceColors      bool     `yaml:"force_colors"`
	InJson           bool     `yaml:"in_json"`
	FilterComponents []string `yaml:"filter_components"`
}

func (c *Log) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Log
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	// applied at startup by logging.Setup, only validated here
	_, err = logging.ParseLevel(c.Level)
	return err
}

func (u *Config) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Config
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if u.Mtls == nil {
		return fmt.Errorf("mtls config: mtls is empty")
	}
	if u.Server == nil {
		return fmt.Errorf("server config: server is empty")
	}
	if u.StoreManager.SqlStorer == nil {
		return fmt.Errorf("store config: store.sql is required")
	}
	if len(u.Instantiators) > 0 {
		names := make(map[string]struct{})
		for _, i := range u.Instantiators {
			if _, ok := names[i.Name]; ok {
				return fmt.Errorf("instantiator config: duplicate name: %s", i.Name)
			}
			names[i.Name] = struct{}{}
		}
	}
	if u.IPAM == nil {
		u.IPAM = &IPAMConfig{}
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

func WriteConfig(cnf *Config, configPath string) error {
	b, err := yaml.Marshal(cnf)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, b, 0644)
}
