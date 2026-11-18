package config

import (
	"fmt"
)

const (
	DefaultSocketPath        = "/tmp/starling-waf.sock"
	DefaultMetricsSocketPath = "/tmp/starling-waf-metrics.sock"
)

type WafConfig struct {
	WafEmbedded bool         `yaml:"embedded"`
	WafExec     *WafExec     `yaml:"exec"`
	WafService  *WafService  `yaml:"service"`
	WafExternal *WafExternal `yaml:"external"`
}

// WafMemoryPercent is the share of the memory of the agent's cgroup a WAF running in it (embedded or exec) gets, the
// rest going to envoy and the agent.
const WafMemoryPercent = 20

func (c *WafConfig) SocketMetricPath() string {
	// same order as the runner of starling-waf: embedded, exec, external, then service
	switch {
	case c.WafEmbedded || c.WafExec != nil:
		return DefaultMetricsSocketPath
	case c.WafExternal != nil:
		return c.WafExternal.MetricsSocketPath
	default:
		return c.WafService.MetricsSocketPath
	}
}

func (c *WafConfig) SocketPath() string {
	// same order as the runner of starling-waf: embedded, exec, external, then service
	switch {
	case c.WafEmbedded || c.WafExec != nil:
		return DefaultSocketPath
	case c.WafExternal != nil:
		return c.WafExternal.SocketPath
	default:
		return c.WafService.SocketPath
	}
}

func (c *WafConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain WafConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.WafExec == nil && c.WafService == nil && c.WafExternal == nil {
		c.WafEmbedded = true
	}
	return nil
}

type WafExec struct {
	BinPath  string `yaml:"bin_path"`
	LogLevel string `yaml:"log_level"`
}

func (c *WafExec) UnmarshalYAML(unmarshal func(any) error) error {
	type plain WafExec
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.BinPath == "" {
		c.BinPath = "starling-waf"
	}
	return nil
}

type WafService struct {
	SocketPath        string   `yaml:"socket_path"`
	MetricsSocketPath string   `yaml:"metrics_socket_path"`
	GetPidCommand     []string `yaml:"get_pid_command"`
	StartCommand      []string `yaml:"start_command"`
	StdoutFilePath    string   `yaml:"stdout_file_path"`
	StderrFilePath    string   `yaml:"stderr_file_path"`
}

func (c *WafService) UnmarshalYAML(unmarshal func(any) error) error {
	type plain WafService
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.SocketPath == "" {
		return fmt.Errorf("starling-waf service socket_path is required")
	}
	if c.MetricsSocketPath == "" {
		return fmt.Errorf("starling-waf service metrics_socket_path is required")
	}
	if len(c.GetPidCommand) == 0 {
		return fmt.Errorf("starling-waf service get_pid_command is required")
	}
	if len(c.StartCommand) == 0 {
		return fmt.Errorf("starling-waf service start_command is required")
	}
	return nil
}

type WafExternal struct {
	SocketPath        string `yaml:"socket_path"`
	MetricsSocketPath string `yaml:"metrics_socket_path"`
}

func (c *WafExternal) UnmarshalYAML(unmarshal func(any) error) error {
	type plain WafExternal
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.SocketPath == "" {
		return fmt.Errorf("starling-waf external socket_path is required")
	}
	if c.MetricsSocketPath == "" {
		return fmt.Errorf("starling-waf external metrics_socket_path is required")
	}
	return nil
}
