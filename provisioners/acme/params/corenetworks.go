package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/corenetworks"
)

type CorenetworksParams struct {
	Login              string `mapstructure:"login"`
	Password           string `mapstructure:"password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *CorenetworksParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return corenetworks.NewDNSProviderConfig(cnf)
}

func (p *CorenetworksParams) ToConfig() (*corenetworks.Config, error) {
	cnf := corenetworks.NewDefaultConfig()
	setString(&cnf.Login, p.Login)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("corenetworks: %w", err)
	}
	return cnf, nil
}
