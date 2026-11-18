package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/gcore"
)

type GcoreParams struct {
	APIToken           string `mapstructure:"api_token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *GcoreParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return gcore.NewDNSProviderConfig(cnf)
}

func (p *GcoreParams) ToConfig() (*gcore.Config, error) {
	cnf := gcore.NewDefaultConfig()
	setString(&cnf.APIToken, p.APIToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("gcore: %w", err)
	}
	return cnf, nil
}
