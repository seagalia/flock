package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/tele3"
)

type Tele3Params struct {
	Key                string `mapstructure:"key"`
	Secret             string `mapstructure:"secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *Tele3Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return tele3.NewDNSProviderConfig(cnf)
}

func (p *Tele3Params) ToConfig() (*tele3.Config, error) {
	cnf := tele3.NewDefaultConfig()
	setString(&cnf.Key, p.Key)
	setString(&cnf.Secret, p.Secret)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("tele3: %w", err)
	}
	return cnf, nil
}
