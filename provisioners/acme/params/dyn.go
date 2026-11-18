package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/dyn"
)

type DynParams struct {
	CustomerName       string `mapstructure:"customer_name"`
	UserName           string `mapstructure:"user_name"`
	Password           string `mapstructure:"password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *DynParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return dyn.NewDNSProviderConfig(cnf)
}

func (p *DynParams) ToConfig() (*dyn.Config, error) {
	cnf := dyn.NewDefaultConfig()
	setString(&cnf.CustomerName, p.CustomerName)
	setString(&cnf.UserName, p.UserName)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("dyn: %w", err)
	}
	return cnf, nil
}
