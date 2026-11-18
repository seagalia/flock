package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/cloudns"
)

type CloudnsParams struct {
	AuthID             string `mapstructure:"auth_id"`
	SubAuthID          string `mapstructure:"sub_auth_id"`
	AuthPassword       string `mapstructure:"auth_password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *CloudnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return cloudns.NewDNSProviderConfig(cnf)
}

func (p *CloudnsParams) ToConfig() (*cloudns.Config, error) {
	cnf := cloudns.NewDefaultConfig()
	setString(&cnf.AuthID, p.AuthID)
	setString(&cnf.SubAuthID, p.SubAuthID)
	setString(&cnf.AuthPassword, p.AuthPassword)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("cloudns: %w", err)
	}
	return cnf, nil
}
