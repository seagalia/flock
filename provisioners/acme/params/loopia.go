package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/loopia"
)

type LoopiaParams struct {
	BaseURL            string `mapstructure:"base_url"`
	APIUser            string `mapstructure:"api_user"`
	APIPassword        string `mapstructure:"api_password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *LoopiaParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return loopia.NewDNSProviderConfig(cnf)
}

func (p *LoopiaParams) ToConfig() (*loopia.Config, error) {
	cnf := loopia.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)
	setString(&cnf.APIUser, p.APIUser)
	setString(&cnf.APIPassword, p.APIPassword)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("loopia: %w", err)
	}
	return cnf, nil
}
