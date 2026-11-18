package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/f5xc"
)

type F5xcParams struct {
	APIToken           string `mapstructure:"api_token"`
	TenantName         string `mapstructure:"tenant_name"`
	Server             string `mapstructure:"server"`
	GroupName          string `mapstructure:"group_name"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *F5xcParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return f5xc.NewDNSProviderConfig(cnf)
}

func (p *F5xcParams) ToConfig() (*f5xc.Config, error) {
	cnf := f5xc.NewDefaultConfig()
	setString(&cnf.APIToken, p.APIToken)
	setString(&cnf.TenantName, p.TenantName)
	setString(&cnf.Server, p.Server)
	setString(&cnf.GroupName, p.GroupName)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("f5xc: %w", err)
	}
	return cnf, nil
}
