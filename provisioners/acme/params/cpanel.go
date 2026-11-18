package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/cpanel"
)

type CpanelParams struct {
	Mode               string `mapstructure:"mode"`
	Username           string `mapstructure:"username"`
	Token              string `mapstructure:"token"`
	BaseURL            string `mapstructure:"base_url"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *CpanelParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return cpanel.NewDNSProviderConfig(cnf)
}

func (p *CpanelParams) ToConfig() (*cpanel.Config, error) {
	cnf := cpanel.NewDefaultConfig()
	setString(&cnf.Mode, p.Mode)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Token, p.Token)
	setString(&cnf.BaseURL, p.BaseURL)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("cpanel: %w", err)
	}
	return cnf, nil
}
