package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/ibmcloud"
)

type IbmcloudParams struct {
	Username           string `mapstructure:"username"`
	APIKey             string `mapstructure:"api_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	HTTPTimeout        string `mapstructure:"http_timeout"`
	Debug              string `mapstructure:"debug"`
}

func (p *IbmcloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return ibmcloud.NewDNSProviderConfig(cnf)
}

func (p *IbmcloudParams) ToConfig() (*ibmcloud.Config, error) {
	cnf := ibmcloud.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.APIKey, p.APIKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.HTTPTimeout, "http_timeout", p.HTTPTimeout),
		setBool(&cnf.Debug, "debug", p.Debug),
	)
	if err != nil {
		return nil, fmt.Errorf("ibmcloud: %w", err)
	}
	return cnf, nil
}
