package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/inwx"
)

type InwxParams struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	SharedSecret       string `mapstructure:"shared_secret"`
	Sandbox            string `mapstructure:"sandbox"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *InwxParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return inwx.NewDNSProviderConfig(cnf)
}

func (p *InwxParams) ToConfig() (*inwx.Config, error) {
	cnf := inwx.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.SharedSecret, p.SharedSecret)

	err := errors.Join(
		setBool(&cnf.Sandbox, "sandbox", p.Sandbox),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("inwx: %w", err)
	}
	return cnf, nil
}
