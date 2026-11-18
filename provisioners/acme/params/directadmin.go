package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/directadmin"
)

type DirectadminParams struct {
	BaseURL            string `mapstructure:"base_url"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	ZoneName           string `mapstructure:"zone_name"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *DirectadminParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return directadmin.NewDNSProviderConfig(cnf)
}

func (p *DirectadminParams) ToConfig() (*directadmin.Config, error) {
	cnf := directadmin.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.ZoneName, p.ZoneName)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("directadmin: %w", err)
	}
	return cnf, nil
}
