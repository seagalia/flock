package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/timewebcloud"
)

type TimewebcloudParams struct {
	AuthToken          string `mapstructure:"auth_token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *TimewebcloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return timewebcloud.NewDNSProviderConfig(cnf)
}

func (p *TimewebcloudParams) ToConfig() (*timewebcloud.Config, error) {
	cnf := timewebcloud.NewDefaultConfig()
	setString(&cnf.AuthToken, p.AuthToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("timewebcloud: %w", err)
	}
	return cnf, nil
}
