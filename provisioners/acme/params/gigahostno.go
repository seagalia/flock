package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/gigahostno"
)

type GigahostnoParams struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	Secret             string `mapstructure:"secret"`
	APIkey             string `mapstructure:"api_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *GigahostnoParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return gigahostno.NewDNSProviderConfig(cnf)
}

func (p *GigahostnoParams) ToConfig() (*gigahostno.Config, error) {
	cnf := gigahostno.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.Secret, p.Secret)
	setString(&cnf.APIkey, p.APIkey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("gigahostno: %w", err)
	}
	return cnf, nil
}
