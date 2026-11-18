package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/easydns"
)

type EasydnsParams struct {
	Endpoint           string `mapstructure:"endpoint"`
	Token              string `mapstructure:"token"`
	Key                string `mapstructure:"key"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
}

func (p *EasydnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return easydns.NewDNSProviderConfig(cnf)
}

func (p *EasydnsParams) ToConfig() (*easydns.Config, error) {
	cnf := easydns.NewDefaultConfig()
	setString(&cnf.Token, p.Token)
	setString(&cnf.Key, p.Key)

	err := errors.Join(
		setURL(&cnf.Endpoint, "endpoint", p.Endpoint),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("easydns: %w", err)
	}
	return cnf, nil
}
