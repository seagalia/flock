package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/virtualname"
)

type VirtualnameParams struct {
	Token              string `mapstructure:"token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *VirtualnameParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return virtualname.NewDNSProviderConfig(cnf)
}

func (p *VirtualnameParams) ToConfig() (*virtualname.Config, error) {
	cnf := virtualname.NewDefaultConfig()
	setString(&cnf.Token, p.Token)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("virtualname: %w", err)
	}
	return cnf, nil
}
