package config

import (
	"fmt"
	"net"
)

type AdvertisementConfig struct {
	BgpConfig                *BGPConfig            `yaml:"bgp"`
	L2Config                 *L2AdvConfig          `yaml:"l2"`
	Interfaces               []string              `yaml:"interfaces"`
	DisableSnat              bool                  `yaml:"disable_snat"`
	AgentList                *AgentListConfig      `yaml:"agent_list"`
	DynamicPlacementStrategy string                `yaml:"dynamic_placement_strategy"`
	NetworkManager           *NetworkManagerConfig `yaml:"network_manager"`
}

func (c *AdvertisementConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain AdvertisementConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	return c.init()
}

func (c *AdvertisementConfig) init() error {
	if len(c.Interfaces) == 0 {
		iface, err := getDefaultInterface()
		if err != nil {
			return err
		}
		c.Interfaces = []string{iface}
	}
	for _, iface := range c.Interfaces {
		_, err := net.InterfaceByName(iface)
		if err != nil {
			return fmt.Errorf("advertisement config: interface %q does not exist: %s", iface, err)
		}
	}
	if c.NetworkManager == nil {
		c.NetworkManager = defaultNetworkManagerConfig()
	}
	return nil
}
