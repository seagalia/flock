package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/volcengine"
)

type VolcengineParams struct {
	AccessKey          string `mapstructure:"access_key"`
	SecretKey          string `mapstructure:"secret_key"`
	Region             string `mapstructure:"region"`
	Host               string `mapstructure:"host"`
	Scheme             string `mapstructure:"scheme"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	HTTPTimeout        string `mapstructure:"http_timeout"`
}

func (p *VolcengineParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return volcengine.NewDNSProviderConfig(cnf)
}

func (p *VolcengineParams) ToConfig() (*volcengine.Config, error) {
	cnf := volcengine.NewDefaultConfig()
	setString(&cnf.AccessKey, p.AccessKey)
	setString(&cnf.SecretKey, p.SecretKey)
	setString(&cnf.Region, p.Region)
	setString(&cnf.Host, p.Host)
	setString(&cnf.Scheme, p.Scheme)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.HTTPTimeout, "http_timeout", p.HTTPTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("volcengine: %w", err)
	}
	return cnf, nil
}
