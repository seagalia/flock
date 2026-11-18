package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/myra"
)

type MyraParams struct {
	APIKey             string `mapstructure:"api_key"`
	APISecret          string `mapstructure:"api_secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *MyraParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return myra.NewDNSProviderConfig(cnf)
}

func (p *MyraParams) ToConfig() (*myra.Config, error) {
	cnf := myra.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.APISecret, p.APISecret)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("myra: %w", err)
	}
	return cnf, nil
}
