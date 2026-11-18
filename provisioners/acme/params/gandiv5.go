package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/gandiv5"
)

type Gandiv5Params struct {
	BaseURL             string `mapstructure:"base_url"`
	APIKey              string `mapstructure:"api_key"`
	PersonalAccessToken string `mapstructure:"personal_access_token"`
	PropagationTimeout  string `mapstructure:"propagation_timeout"`
	PollingInterval     string `mapstructure:"polling_interval"`
	TTL                 string `mapstructure:"ttl"`
}

func (p *Gandiv5Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return gandiv5.NewDNSProviderConfig(cnf)
}

func (p *Gandiv5Params) ToConfig() (*gandiv5.Config, error) {
	cnf := gandiv5.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.PersonalAccessToken, p.PersonalAccessToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("gandiv5: %w", err)
	}
	return cnf, nil
}
