package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/infomaniak"
)

type InfomaniakParams struct {
	APIEndpoint        string `mapstructure:"api_endpoint"`
	AccessToken        string `mapstructure:"access_token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *InfomaniakParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return infomaniak.NewDNSProviderConfig(cnf)
}

func (p *InfomaniakParams) ToConfig() (*infomaniak.Config, error) {
	cnf := infomaniak.NewDefaultConfig()
	setString(&cnf.APIEndpoint, p.APIEndpoint)
	setString(&cnf.AccessToken, p.AccessToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("infomaniak: %w", err)
	}
	return cnf, nil
}
