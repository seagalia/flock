package config

import (
	"fmt"
	"github.com/seagalia/flock/sdk/flock/api/config/agent/v1"
	"github.com/seagalia/flock/sdk/helpers"
	"strings"
)

type ResourceType struct {
	Name string `yaml:"name"`
	// cpu in mhz
	CPU int `yaml:"cpu"`
	// Memory in Mo
	Memory int64 `yaml:"memory"`
	// Disk in Mo (we don't need too much)
	Disk   int `yaml:"disk"`
	Number int `yaml:"number"`
}
type Instantiator struct {
	Name                string              `yaml:"name"`
	Zones               []string            `yaml:"zones"`
	OriginatingIdentity string              `yaml:"originating_identity"`
	ResourceTypes       []*ResourceType     `yaml:"resource_types"`
	StaticInstantiator  *StaticInstantiator `yaml:"static"`
}

func (u *Instantiator) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Instantiator
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if u.Name == "" {
		return fmt.Errorf("instantiator config: name is required")
	}
	if len(u.Zones) == 0 {
		return fmt.Errorf("instantiator config: at least one zone is required")
	}
	if len(u.ResourceTypes) == 0 {
		return fmt.Errorf("instantiator config: at least one resource_type is required")
	}
	if u.StaticInstantiator == nil {
		return fmt.Errorf("instantiator config: at least one instantiator is required")
	}
	if u.OriginatingIdentity == "" {
		u.OriginatingIdentity = "controller"
	}
	return nil
}

type AgentMode string

func (m *AgentMode) UnmarshalYAML(unmarshal func(any) error) error {
	var mode string
	err := unmarshal(&mode)
	if err != nil {
		return err
	}
	if mode == "" {
		mode = agent.Agent_Mode_name[0]
	}
	mode = strings.ToUpper(mode)
	_, ok := agent.Agent_Mode_value[mode]
	if !ok {
		return fmt.Errorf("unknown agent mode %s", mode)
	}
	*m = AgentMode(mode)
	return nil
}

func (m *AgentMode) ToAgentMode() agent.Agent_Mode {
	if m == nil {
		return agent.Agent_Mode(0)
	}
	return agent.Agent_Mode(agent.Agent_Mode_value[string(*m)])
}

type AgentInstantiator struct {
	UUID       string     `yaml:"uuid"`
	Name       string     `yaml:"name"`
	Host       string     `yaml:"host"`
	Type       string     `yaml:"type"`
	Extensions []string   `yaml:"extensions"`
	Mode       *AgentMode `yaml:"mode"`
	Features   struct {
		ShowLogs    bool `yaml:"show_logs"`
		ShowMetrics bool `yaml:"show_metrics"`
		ShowStatus  bool `yaml:"show_status"`
		ShowL3Log   bool `yaml:"show_l3_log"`
	} `yaml:"features"`
}

func (u *AgentInstantiator) UnmarshalYAML(unmarshal func(any) error) error {
	type plain AgentInstantiator
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if u.UUID == "" {
		return fmt.Errorf("agent config: uuid is required")
	}
	if u.Name == "" {
		return fmt.Errorf("agent config: name is required")
	}
	if !helpers.AgentNameRule.MatchString(u.Name) {
		return fmt.Errorf("agent config: name %q must match %s", u.Name, helpers.AgentNamePattern)
	}
	if u.Host == "" {
		return fmt.Errorf("agent config: host is required")
	}
	if u.Type == "" {
		u.Type = "starling"
	}
	if u.Extensions == nil {
		u.Extensions = []string{}
	}
	return nil
}

func (u *AgentInstantiator) ToAgent() *agent.Agent {
	return &agent.Agent{
		UUID:       u.UUID,
		Name:       u.Name,
		Host:       u.Host,
		Type:       u.Type,
		Extensions: u.Extensions,
		Mode:       u.Mode.ToAgentMode(),
		Features: &agent.AgentFeatures{
			ShowLogs:    u.Features.ShowLogs,
			ShowMetrics: u.Features.ShowMetrics,
			ShowStatus:  u.Features.ShowStatus,
			ShowL3Log:   u.Features.ShowL3Log,
		},
	}
}

type StaticInstantiator struct {
	Agents []*AgentInstantiator `yaml:"agents"`
}

func (u *StaticInstantiator) UnmarshalYAML(unmarshal func(any) error) error {
	type plain StaticInstantiator
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if len(u.Agents) == 0 {
		return fmt.Errorf("static instantiator config: at least one agent is required")
	}
	names := make(map[string]struct{}, len(u.Agents))
	for _, ag := range u.Agents {
		if _, seen := names[ag.Name]; seen {
			return fmt.Errorf("static instantiator config: agent name %q is declared twice", ag.Name)
		}
		names[ag.Name] = struct{}{}
	}
	return nil
}
