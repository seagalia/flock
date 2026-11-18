package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/ultradns"
)

type UltradnsParams struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	Endpoint           string `mapstructure:"endpoint"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *UltradnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return ultradns.NewDNSProviderConfig(cnf)
}

func (p *UltradnsParams) ToConfig() (*ultradns.Config, error) {
	cnf := ultradns.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.Endpoint, p.Endpoint)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("ultradns: %w", err)
	}
	return cnf, nil
}
