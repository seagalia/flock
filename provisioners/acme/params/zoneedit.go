package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/zoneedit"
)

type ZoneeditParams struct {
	User               string `mapstructure:"user"`
	AuthToken          string `mapstructure:"auth_token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *ZoneeditParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return zoneedit.NewDNSProviderConfig(cnf)
}

func (p *ZoneeditParams) ToConfig() (*zoneedit.Config, error) {
	cnf := zoneedit.NewDefaultConfig()
	setString(&cnf.User, p.User)
	setString(&cnf.AuthToken, p.AuthToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("zoneedit: %w", err)
	}
	return cnf, nil
}
