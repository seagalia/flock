package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/netcupv1"
)

type Netcupv1Params struct {
	APIKey             string `mapstructure:"api_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *Netcupv1Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return netcupv1.NewDNSProviderConfig(cnf)
}

func (p *Netcupv1Params) ToConfig() (*netcupv1.Config, error) {
	cnf := netcupv1.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("netcupv1: %w", err)
	}
	return cnf, nil
}
