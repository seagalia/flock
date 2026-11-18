package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/active24"
)

type Active24Params struct {
	APIKey             string `mapstructure:"api_key"`
	Secret             string `mapstructure:"secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *Active24Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return active24.NewDNSProviderConfig(cnf)
}

func (p *Active24Params) ToConfig() (*active24.Config, error) {
	cnf := active24.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.Secret, p.Secret)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("active24: %w", err)
	}
	return cnf, nil
}
