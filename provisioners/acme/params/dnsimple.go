package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/dnsimple"
)

type DnsimpleParams struct {
	Debug              string `mapstructure:"debug"`
	AccessToken        string `mapstructure:"access_token"`
	BaseURL            string `mapstructure:"base_url"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *DnsimpleParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return dnsimple.NewDNSProviderConfig(cnf)
}

func (p *DnsimpleParams) ToConfig() (*dnsimple.Config, error) {
	cnf := dnsimple.NewDefaultConfig()
	setString(&cnf.AccessToken, p.AccessToken)
	setString(&cnf.BaseURL, p.BaseURL)

	err := errors.Join(
		setBool(&cnf.Debug, "debug", p.Debug),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("dnsimple: %w", err)
	}
	return cnf, nil
}
