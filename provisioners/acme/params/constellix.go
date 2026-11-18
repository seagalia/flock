package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/constellix"
)

type ConstellixParams struct {
	APIKey             string `mapstructure:"api_key"`
	SecretKey          string `mapstructure:"secret_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *ConstellixParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return constellix.NewDNSProviderConfig(cnf)
}

func (p *ConstellixParams) ToConfig() (*constellix.Config, error) {
	cnf := constellix.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.SecretKey, p.SecretKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("constellix: %w", err)
	}
	return cnf, nil
}
