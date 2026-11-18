package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/safedns"
)

type SafednsParams struct {
	AuthToken          string `mapstructure:"auth_token"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *SafednsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return safedns.NewDNSProviderConfig(cnf)
}

func (p *SafednsParams) ToConfig() (*safedns.Config, error) {
	cnf := safedns.NewDefaultConfig()
	setString(&cnf.AuthToken, p.AuthToken)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("safedns: %w", err)
	}
	return cnf, nil
}
