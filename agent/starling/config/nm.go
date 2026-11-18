package config

const (
	snatMark    = 15181
	snatLogMark = 15182
	logOnlyMark = 15183
	// DefaultDummyInterfaceName is the interface holding the vips
	DefaultDummyInterfaceName = "flock0"
)

type NetworkManagerConfig struct {
	SnatMark           uint   `yaml:"snat_mark"`
	SnatLogMark        uint   `yaml:"snat_log_mark"`
	LogOnlyMark        uint   `yaml:"log_only_mark"`
	DummyInterfaceName string `yaml:"dummy_interface_name"`
	DisableInitRules   bool   `yaml:"disable_init_rules"`
}

func (u *NetworkManagerConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain NetworkManagerConfig
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}

	if u.SnatMark == 0 {
		u.SnatMark = snatMark
	}
	if u.SnatLogMark == 0 {
		u.SnatLogMark = snatLogMark
	}
	if u.LogOnlyMark == 0 {
		u.LogOnlyMark = logOnlyMark
	}
	if u.DummyInterfaceName == "" {
		u.DummyInterfaceName = DefaultDummyInterfaceName
	}
	return nil
}

func defaultNetworkManagerConfig() *NetworkManagerConfig {
	return &NetworkManagerConfig{
		SnatMark:           snatMark,
		SnatLogMark:        snatLogMark,
		LogOnlyMark:        logOnlyMark,
		DummyInterfaceName: DefaultDummyInterfaceName,
	}
}
