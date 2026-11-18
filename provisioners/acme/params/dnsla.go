package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/dnsla"
)

type DnslaParams struct {
	APIID              string `mapstructure:"api_id"`
	APISecret          string `mapstructure:"api_secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *DnslaParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return dnsla.NewDNSProviderConfig(cnf)
}

func (p *DnslaParams) ToConfig() (*dnsla.Config, error) {
	cnf := dnsla.NewDefaultConfig()
	setString(&cnf.APIID, p.APIID)
	setString(&cnf.APISecret, p.APISecret)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("dnsla: %w", err)
	}
	return cnf, nil
}
