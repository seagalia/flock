package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/namecheap"
)

type NamecheapParams struct {
	BaseURL            string `mapstructure:"base_url"`
	APIUser            string `mapstructure:"api_user"`
	APIKey             string `mapstructure:"api_key"`
	ClientIP           string `mapstructure:"client_ip"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *NamecheapParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return namecheap.NewDNSProviderConfig(cnf)
}

func (p *NamecheapParams) ToConfig() (*namecheap.Config, error) {
	cnf := namecheap.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)
	setString(&cnf.APIUser, p.APIUser)
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.ClientIP, p.ClientIP)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("namecheap: %w", err)
	}
	return cnf, nil
}
