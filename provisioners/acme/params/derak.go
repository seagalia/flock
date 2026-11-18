package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/derak"
)

type DerakParams struct {
	APIKey             string `mapstructure:"api_key"`
	WebsiteID          string `mapstructure:"website_id"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *DerakParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return derak.NewDNSProviderConfig(cnf)
}

func (p *DerakParams) ToConfig() (*derak.Config, error) {
	cnf := derak.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.WebsiteID, p.WebsiteID)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("derak: %w", err)
	}
	return cnf, nil
}
