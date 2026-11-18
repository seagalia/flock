package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/godaddyv3"
)

type Godaddyv3Params struct {
	PersonalAccessToken string `mapstructure:"personal_access_token"`
	PropagationTimeout  string `mapstructure:"propagation_timeout"`
	PollingInterval     string `mapstructure:"polling_interval"`
	TTL                 string `mapstructure:"ttl"`
}

func (p *Godaddyv3Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return godaddyv3.NewDNSProviderConfig(cnf)
}

func (p *Godaddyv3Params) ToConfig() (*godaddyv3.Config, error) {
	cnf := godaddyv3.NewDefaultConfig()
	setString(&cnf.PersonalAccessToken, p.PersonalAccessToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("godaddyv3: %w", err)
	}
	return cnf, nil
}
