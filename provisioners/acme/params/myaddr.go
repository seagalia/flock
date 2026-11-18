package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/myaddr"
)

type MyaddrParams struct {
	Credentials        string `mapstructure:"credentials"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *MyaddrParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return myaddr.NewDNSProviderConfig(cnf)
}

func (p *MyaddrParams) ToConfig() (*myaddr.Config, error) {
	cnf := myaddr.NewDefaultConfig()

	err := errors.Join(
		setPairs(&cnf.Credentials, "credentials", p.Credentials),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("myaddr: %w", err)
	}
	return cnf, nil
}
