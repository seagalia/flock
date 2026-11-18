package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/alidns"
)

type AlidnsParams struct {
	RAMRole            string `mapstructure:"ram_role"`
	APIKey             string `mapstructure:"api_key"`
	SecretKey          string `mapstructure:"secret_key"`
	SecurityToken      string `mapstructure:"security_token"`
	RegionID           string `mapstructure:"region_id"`
	Line               string `mapstructure:"line"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	HTTPTimeout        string `mapstructure:"http_timeout"`
}

func (p *AlidnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return alidns.NewDNSProviderConfig(cnf)
}

func (p *AlidnsParams) ToConfig() (*alidns.Config, error) {
	cnf := alidns.NewDefaultConfig()
	setString(&cnf.RAMRole, p.RAMRole)
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.SecretKey, p.SecretKey)
	setString(&cnf.SecurityToken, p.SecurityToken)
	setString(&cnf.RegionID, p.RegionID)
	setString(&cnf.Line, p.Line)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.HTTPTimeout, "http_timeout", p.HTTPTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("alidns: %w", err)
	}
	return cnf, nil
}
