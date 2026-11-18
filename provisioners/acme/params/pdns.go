package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/pdns"
)

type PdnsParams struct {
	APIKey             string `mapstructure:"api_key"`
	Host               string `mapstructure:"host"`
	ServerName         string `mapstructure:"server_name"`
	APIVersion         string `mapstructure:"api_version"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *PdnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return pdns.NewDNSProviderConfig(cnf)
}

func (p *PdnsParams) ToConfig() (*pdns.Config, error) {
	cnf := pdns.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.ServerName, p.ServerName)

	err := errors.Join(
		setURL(&cnf.Host, "host", p.Host),
		setInt(&cnf.APIVersion, "api_version", p.APIVersion),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("pdns: %w", err)
	}
	return cnf, nil
}
