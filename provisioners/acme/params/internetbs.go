package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/internetbs"
)

type InternetbsParams struct {
	APIKey             string `mapstructure:"api_key"`
	Password           string `mapstructure:"password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *InternetbsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return internetbs.NewDNSProviderConfig(cnf)
}

func (p *InternetbsParams) ToConfig() (*internetbs.Config, error) {
	cnf := internetbs.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("internetbs: %w", err)
	}
	return cnf, nil
}
