package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/httpnet"
)

type HttpnetParams struct {
	APIKey             string `mapstructure:"api_key"`
	ZoneName           string `mapstructure:"zone_name"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *HttpnetParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return httpnet.NewDNSProviderConfig(cnf)
}

func (p *HttpnetParams) ToConfig() (*httpnet.Config, error) {
	cnf := httpnet.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.ZoneName, p.ZoneName)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("httpnet: %w", err)
	}
	return cnf, nil
}
