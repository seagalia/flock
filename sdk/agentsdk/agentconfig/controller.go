package agentconfig

import "fmt"

type ControllerConfig struct {
	Addr         string `yaml:"addr"`
	ServerName   string `yaml:"server_name"`
	InstanceUuid string `yaml:"instance_uuid"`
}

func (c *ControllerConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain ControllerConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.Addr == "" {
		return fmt.Errorf("controller addr is required")
	}
	if c.InstanceUuid == "" {
		return fmt.Errorf("controller instance_uuid is required")
	}
	return nil
}
