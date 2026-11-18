package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/mythicbeasts"
)

type MythicbeastsParams struct {
	UserName           string `mapstructure:"user_name"`
	Password           string `mapstructure:"password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	APIEndpoint        string `mapstructure:"api_endpoint"`
	AuthAPIEndpoint    string `mapstructure:"auth_api_endpoint"`
	TTL                string `mapstructure:"ttl"`
}

func (p *MythicbeastsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return mythicbeasts.NewDNSProviderConfig(cnf)
}

func (p *MythicbeastsParams) ToConfig() (*mythicbeasts.Config, error) {
	cnf, err := mythicbeasts.NewDefaultConfig()
	if err != nil {
		return nil, err
	}
	setString(&cnf.UserName, p.UserName)
	setString(&cnf.Password, p.Password)

	err = errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setURL(&cnf.APIEndpoint, "api_endpoint", p.APIEndpoint),
		setURL(&cnf.AuthAPIEndpoint, "auth_api_endpoint", p.AuthAPIEndpoint),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("mythicbeasts: %w", err)
	}
	return cnf, nil
}
