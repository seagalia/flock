package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/conoha"
)

type ConohaParams struct {
	Region             string `mapstructure:"region"`
	TenantID           string `mapstructure:"tenant_id"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *ConohaParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return conoha.NewDNSProviderConfig(cnf)
}

func (p *ConohaParams) ToConfig() (*conoha.Config, error) {
	cnf := conoha.NewDefaultConfig()
	setString(&cnf.Region, p.Region)
	setString(&cnf.TenantID, p.TenantID)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("conoha: %w", err)
	}
	return cnf, nil
}
