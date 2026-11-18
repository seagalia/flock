package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/hostinger"
)

type HostingerParams struct {
	APIToken           string `mapstructure:"api_token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *HostingerParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return hostinger.NewDNSProviderConfig(cnf)
}

func (p *HostingerParams) ToConfig() (*hostinger.Config, error) {
	cnf := hostinger.NewDefaultConfig()
	setString(&cnf.APIToken, p.APIToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("hostinger: %w", err)
	}
	return cnf, nil
}
