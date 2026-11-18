package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/wannafind"
)

type WannafindParams struct {
	ClientID           string `mapstructure:"client_id"`
	ClientSecret       string `mapstructure:"client_secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	APIKey             string `mapstructure:"api_key"`
}

func (p *WannafindParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return wannafind.NewDNSProviderConfig(cnf)
}

func (p *WannafindParams) ToConfig() (*wannafind.Config, error) {
	cnf := wannafind.NewDefaultConfig()
	setString(&cnf.ClientID, p.ClientID)
	setString(&cnf.ClientSecret, p.ClientSecret)
	setString(&cnf.APIKey, p.APIKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("wannafind: %w", err)
	}
	return cnf, nil
}
