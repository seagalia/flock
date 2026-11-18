package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/azion"
)

type AzionParams struct {
	PersonalToken      string `mapstructure:"personal_token"`
	PageSize           string `mapstructure:"page_size"`
	PollingInterval    string `mapstructure:"polling_interval"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	TTL                string `mapstructure:"ttl"`
}

func (p *AzionParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return azion.NewDNSProviderConfig(cnf)
}

func (p *AzionParams) ToConfig() (*azion.Config, error) {
	cnf := azion.NewDefaultConfig()
	setString(&cnf.PersonalToken, p.PersonalToken)

	err := errors.Join(
		setInt(&cnf.PageSize, "page_size", p.PageSize),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("azion: %w", err)
	}
	return cnf, nil
}
