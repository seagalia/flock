package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/gravity"
)

type GravityParams struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	ServerURL          string `mapstructure:"server_url"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
}

func (p *GravityParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return gravity.NewDNSProviderConfig(cnf)
}

func (p *GravityParams) ToConfig() (*gravity.Config, error) {
	cnf := gravity.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.ServerURL, p.ServerURL)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("gravity: %w", err)
	}
	return cnf, nil
}
