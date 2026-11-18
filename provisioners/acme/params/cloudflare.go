package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/cloudflare"
)

type CloudflareParams struct {
	AuthEmail          string `mapstructure:"auth_email"`
	AuthKey            string `mapstructure:"auth_key"`
	AuthToken          string `mapstructure:"auth_token"`
	ZoneToken          string `mapstructure:"zone_token"`
	BaseURL            string `mapstructure:"base_url"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *CloudflareParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return cloudflare.NewDNSProviderConfig(cnf)
}

func (p *CloudflareParams) ToConfig() (*cloudflare.Config, error) {
	cnf := cloudflare.NewDefaultConfig()
	setString(&cnf.AuthEmail, p.AuthEmail)
	setString(&cnf.AuthKey, p.AuthKey)
	setString(&cnf.AuthToken, p.AuthToken)
	setString(&cnf.ZoneToken, p.ZoneToken)
	setString(&cnf.BaseURL, p.BaseURL)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("cloudflare: %w", err)
	}
	return cnf, nil
}
