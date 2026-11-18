package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/liquidweb"
)

type LiquidwebParams struct {
	BaseURL            string `mapstructure:"base_url"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	Zone               string `mapstructure:"zone"`
	TTL                string `mapstructure:"ttl"`
	PollingInterval    string `mapstructure:"polling_interval"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	HTTPTimeout        string `mapstructure:"http_timeout"`
}

func (p *LiquidwebParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return liquidweb.NewDNSProviderConfig(cnf)
}

func (p *LiquidwebParams) ToConfig() (*liquidweb.Config, error) {
	cnf := liquidweb.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.Zone, p.Zone)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.HTTPTimeout, "http_timeout", p.HTTPTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("liquidweb: %w", err)
	}
	return cnf, nil
}
