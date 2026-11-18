package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/dnshomede"
)

type DnshomedeParams struct {
	Credentials        string `mapstructure:"credentials"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
}

func (p *DnshomedeParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return dnshomede.NewDNSProviderConfig(cnf)
}

func (p *DnshomedeParams) ToConfig() (*dnshomede.Config, error) {
	cnf := dnshomede.NewDefaultConfig()

	err := errors.Join(
		setPairs(&cnf.Credentials, "credentials", p.Credentials),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("dnshomede: %w", err)
	}
	return cnf, nil
}
