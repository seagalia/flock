package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/nicru"
)

type NicruParams struct {
	TTL                string `mapstructure:"ttl"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	ServiceID          string `mapstructure:"service_id"`
	Secret             string `mapstructure:"secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *NicruParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return nicru.NewDNSProviderConfig(cnf)
}

func (p *NicruParams) ToConfig() (*nicru.Config, error) {
	cnf := nicru.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.ServiceID, p.ServiceID)
	setString(&cnf.Secret, p.Secret)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("nicru: %w", err)
	}
	return cnf, nil
}
