package agentconfig

import "fmt"

type TlsConfig struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CaFile   string `yaml:"ca_file"`
}

func (c *TlsConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain TlsConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.CertFile == "" {
		return fmt.Errorf("tls cert_file is required")
	}
	if c.KeyFile == "" {
		return fmt.Errorf("tls key_file is required")
	}
	return nil
}
