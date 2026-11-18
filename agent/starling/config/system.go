package config

import "fmt"

type SystemConfig struct {
	ExtraSysctl map[string]string `yaml:"sysctl"`
	SysctlMode  SysctlMode        `yaml:"sysctl_mode"`
}

func (c *SystemConfig) init() error {
	c.SysctlMode = SysctlModeConfigure
	return nil
}

type SysctlMode string

const (
	SysctlModeValidate   SysctlMode = "validate"
	SysctlModeConfigure  SysctlMode = "configure"
	SysctlModePermissive SysctlMode = "permissive"
	SysctlModeDisabled   SysctlMode = "disabled"
)

func (u *SysctlMode) UnmarshalYAML(unmarshal func(any) error) error {
	var raw string
	err := unmarshal(&raw)
	if err != nil {
		return err
	}
	*u = SysctlMode(raw)
	if *u == "" {
		*u = SysctlModeConfigure
	}

	switch *u {
	case SysctlModeValidate, SysctlModeConfigure, SysctlModePermissive, SysctlModeDisabled:
		break
	default:
		return fmt.Errorf("invalid sysctl mode: %s", *u)
	}
	return nil
}
