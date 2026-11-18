package config

import (
	"fmt"
	"time"

	"github.com/seagalia/flock/sdk/agentsdk/leases"
)

const (
	defaultCADirUrl = "https://acme-v02.api.letsencrypt.org/directory"
)

type AcmeConfig struct {
	Enabled         bool       `yaml:"enabled"`
	DefaultCADirUrl string     `yaml:"default_ca_dir_url"`
	Challenges      Challenges `yaml:"challenges"`
	// LeaseTTL is the life of the lease a replica takes on a certificate before ordering it, between 10s and 5m like the
	// controller grants it. A replica refused the lease of a first order retries when it ends.
	LeaseTTL         time.Duration  `yaml:"lease_ttl"`
	DefaultDnsConfig map[string]any `yaml:"default_dns_config"`
	DefaultEmail     string         `yaml:"default_email"`
}

func (c *AcmeConfig) UnmarshalYAML(unmarshal func(any) error) error {
	type plain AcmeConfig
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}
	if c.DefaultCADirUrl == "" {
		c.DefaultCADirUrl = defaultCADirUrl
	}
	if c.LeaseTTL == 0 {
		c.LeaseTTL = leases.DefaultTTL
	}
	isLeaseTTLInBounds := c.LeaseTTL >= leases.MinTTL && c.LeaseTTL <= leases.MaxTTL
	if !isLeaseTTLInBounds {
		return fmt.Errorf("lease_ttl must be between %s and %s, got %s", leases.MinTTL, leases.MaxTTL, c.LeaseTTL)
	}
	return nil
}

type Challenges struct {
	Http01Enabled    bool   `yaml:"http01_enabled"`
	TlsAlpn01Enabled bool   `yaml:"tls_alpn_01_enabled"`
	Host             string `yaml:"host"`
	TlsAlpn01Port    uint   `yaml:"tls_port"`
	Http01Port       uint   `yaml:"http_port"`
	// Dns01SkipPropagationCheck trusts the DNS provider once it took the TXT record, instead of querying the
	// authoritative servers until they serve it, for a DNS server the provisioner cannot reach or one that serves the
	// record at once
	Dns01SkipPropagationCheck bool `yaml:"dns01_skip_propagation_check"`
}

func (c *Challenges) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Challenges
	err := unmarshal((*plain)(c))
	if err != nil {
		return err
	}

	if c.TlsAlpn01Port == 0 {
		c.TlsAlpn01Port = 443
	}

	if c.Http01Port == 0 {
		c.Http01Port = 80
	}

	if c.Host == "" {
		c.Host = "0.0.0.0"
	}

	if c.Http01Enabled && c.TlsAlpn01Enabled && c.Http01Port == c.TlsAlpn01Port {
		return fmt.Errorf("http01 and tls-alpn-01 ports must be different")
	}
	return nil
}
