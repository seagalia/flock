package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/conohav3"
)

type Conohav3Params struct {
	Region             string `mapstructure:"region"`
	TenantID           string `mapstructure:"tenant_id"`
	UserID             string `mapstructure:"user_id"`
	Password           string `mapstructure:"password"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *Conohav3Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return conohav3.NewDNSProviderConfig(cnf)
}

func (p *Conohav3Params) ToConfig() (*conohav3.Config, error) {
	cnf := conohav3.NewDefaultConfig()
	setString(&cnf.Region, p.Region)
	setString(&cnf.TenantID, p.TenantID)
	setString(&cnf.UserID, p.UserID)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("conohav3: %w", err)
	}
	return cnf, nil
}
