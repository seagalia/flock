package config

import (
	"fmt"
)

// DefaultEnvoyMaxConnections is half the open files the envoy unit of the repository allows (100000), as envoy
// recommends, since its upstream connections take files too.
const DefaultEnvoyMaxConnections = 50000

type EnvoyConfig struct {
	EnvoyExec      *EnvoyExec    `yaml:"exec"`
	EnvoyService   *EnvoyService `yaml:"service"`
	LogInJson      *bool         `yaml:"log_in_json"`
	MaxConnections uint32        `yaml:"max_connections"`
}

func (c *EnvoyConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain EnvoyConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.EnvoyExec == nil && c.EnvoyService == nil {
		c.EnvoyExec = &EnvoyExec{
			BinPath: "envoy",
		}
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = DefaultEnvoyMaxConnections
	}
	return nil
}

type EnvoyExec struct {
	BinPath  string `yaml:"bin_path"`
	LogLevel string `yaml:"log_level"`
}

func (c *EnvoyExec) UnmarshalYAML(unmarshal func(any) error) error {
	type plain EnvoyExec
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.BinPath == "" {
		c.BinPath = "envoy"
	}
	return nil
}

type EnvoyService struct {
	ConfigFilePath string   `yaml:"config_file_path"`
	GetPidCommand  []string `yaml:"get_pid_command"`
	StartCommand   []string `yaml:"start_command"`
	StdoutFilePath string   `yaml:"stdout_file_path"`
	StderrFilePath string   `yaml:"stderr_file_path"`
}

func (c *EnvoyService) UnmarshalYAML(unmarshal func(any) error) error {
	type plain EnvoyService
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.ConfigFilePath == "" {
		return fmt.Errorf("envoy service config_file_path is required")
	}
	if len(c.GetPidCommand) == 0 {
		return fmt.Errorf("envoy service get_pid_command is required")
	}
	if len(c.StartCommand) == 0 {
		return fmt.Errorf("envoy service start_command is required")
	}
	return nil
}
