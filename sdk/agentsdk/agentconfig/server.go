package agentconfig

import (
	"errors"
	"fmt"
)

type ServerConfig struct {
	ListenHost      string `yaml:"listen_host"`
	ListenInterface string `yaml:"listen_interface"`
	ListenPort      uint16 `yaml:"listen_port"`
}

func (u *ServerConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain ServerConfig
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	return u.init()
}

// DefaultServerConfig is the server config of an agent whose config has no server block.
func DefaultServerConfig() (*ServerConfig, error) {
	u := &ServerConfig{}
	return u, u.init()
}

func (u *ServerConfig) init() error {
	if u.ListenPort == 0 {
		u.ListenPort = 18000
	}
	if u.ListenHost == "" {
		outIp, err := OutboundIP()
		if err == nil {
			u.ListenHost = outIp.String()
		}
	}
	var err error
	if u.ListenInterface != "" {
		u.ListenHost, err = FirstAddrOfInterface(u.ListenInterface)
		if err != nil {
			return fmt.Errorf("error getting first address of interface: %w", err)
		}
	}
	if u.ListenHost == "" {
		return errors.New("server listen_host is required")
	}
	return nil
}
