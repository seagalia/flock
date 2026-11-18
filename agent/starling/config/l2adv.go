package config

import (
	"fmt"

	"github.com/seagalia/flock/sdk/agentsdk/agentconfig"
)

type L2AdvConfig struct {
	DisableGratuitous   bool `yaml:"disable_gratuitous"`
	AllInterfaces       bool `yaml:"all_interfaces"`
	UseDynamicPlacement bool `yaml:"use_dynamic_placement"`
}

func (c *L2AdvConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain L2AdvConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	return nil
}

type AgentListConfig struct {
	ListenInterface string   `yaml:"listen_interface"`
	ListenHost      string   `yaml:"listen_host"`
	ListenPort      uint16   `yaml:"listen_port"`
	Secret          string   `yaml:"secret"`
	Members         []string `yaml:"members"`
}

func (u *AgentListConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain AgentListConfig
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if len(u.Members) == 0 {
		return fmt.Errorf("agent list members is empty")
	}
	if u.ListenPort == 0 {
		u.ListenPort = 18001
	}
	if u.ListenHost == "" {
		outIp, err := agentconfig.OutboundIP()
		if err == nil {
			u.ListenHost = outIp.String()
		}
	}
	if u.ListenInterface != "" {
		u.ListenHost, err = agentconfig.FirstAddrOfInterface(u.ListenInterface)
		if err != nil {
			return fmt.Errorf("error getting first address of interface: %w", err)
		}
	}
	if u.ListenHost == "" {
		return fmt.Errorf("agent list listen_host is required")
	}
	return nil
}
