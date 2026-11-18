package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/ispconfigddns"
)

type IspconfigddnsParams struct {
	ServerURL          string `mapstructure:"server_url"`
	Token              string `mapstructure:"token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *IspconfigddnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return ispconfigddns.NewDNSProviderConfig(cnf)
}

func (p *IspconfigddnsParams) ToConfig() (*ispconfigddns.Config, error) {
	cnf := ispconfigddns.NewDefaultConfig()
	setString(&cnf.ServerURL, p.ServerURL)
	setString(&cnf.Token, p.Token)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("ispconfigddns: %w", err)
	}
	return cnf, nil
}
