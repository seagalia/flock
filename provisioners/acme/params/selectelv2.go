package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/selectelv2"
)

type Selectelv2Params struct {
	BaseURL            string `mapstructure:"base_url"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	DomainName         string `mapstructure:"domain_name"`
	ProjectID          string `mapstructure:"project_id"`
	AuthURL            string `mapstructure:"auth_url"`
	AuthRegion         string `mapstructure:"auth_region"`
	UserDomainName     string `mapstructure:"user_domain_name"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *Selectelv2Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return selectelv2.NewDNSProviderConfig(cnf)
}

func (p *Selectelv2Params) ToConfig() (*selectelv2.Config, error) {
	cnf := selectelv2.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.DomainName, p.DomainName)
	setString(&cnf.ProjectID, p.ProjectID)
	setString(&cnf.AuthURL, p.AuthURL)
	setString(&cnf.AuthRegion, p.AuthRegion)
	setString(&cnf.UserDomainName, p.UserDomainName)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("selectelv2: %w", err)
	}
	return cnf, nil
}
