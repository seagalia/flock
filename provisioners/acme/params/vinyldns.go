package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/vinyldns"
)

type VinyldnsParams struct {
	AccessKey          string `mapstructure:"access_key"`
	SecretKey          string `mapstructure:"secret_key"`
	Host               string `mapstructure:"host"`
	QuoteValue         string `mapstructure:"quote_value"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *VinyldnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return vinyldns.NewDNSProviderConfig(cnf)
}

func (p *VinyldnsParams) ToConfig() (*vinyldns.Config, error) {
	cnf := vinyldns.NewDefaultConfig()
	setString(&cnf.AccessKey, p.AccessKey)
	setString(&cnf.SecretKey, p.SecretKey)
	setString(&cnf.Host, p.Host)

	err := errors.Join(
		setBool(&cnf.QuoteValue, "quote_value", p.QuoteValue),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("vinyldns: %w", err)
	}
	return cnf, nil
}
