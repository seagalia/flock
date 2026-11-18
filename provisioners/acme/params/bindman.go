package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/bindman"
)

type BindmanParams struct {
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	BaseURL            string `mapstructure:"base_url"`
}

func (p *BindmanParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return bindman.NewDNSProviderConfig(cnf)
}

func (p *BindmanParams) ToConfig() (*bindman.Config, error) {
	cnf := bindman.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("bindman: %w", err)
	}
	return cnf, nil
}
