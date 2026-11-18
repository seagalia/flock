package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/dode"
)

type DodeParams struct {
	Token              string `mapstructure:"token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
}

func (p *DodeParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return dode.NewDNSProviderConfig(cnf)
}

func (p *DodeParams) ToConfig() (*dode.Config, error) {
	cnf := dode.NewDefaultConfig()
	setString(&cnf.Token, p.Token)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("dode: %w", err)
	}
	return cnf, nil
}
