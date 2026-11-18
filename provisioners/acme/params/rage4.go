package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/rage4"
)

type Rage4Params struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *Rage4Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return rage4.NewDNSProviderConfig(cnf)
}

func (p *Rage4Params) ToConfig() (*rage4.Config, error) {
	cnf := rage4.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("rage4: %w", err)
	}
	return cnf, nil
}
