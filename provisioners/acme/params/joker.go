package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/joker"
)

type JokerParams struct {
	APIKey             string `mapstructure:"api_key"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	APIMode            string `mapstructure:"api_mode"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *JokerParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return joker.NewDNSProviderConfig(cnf)
}

func (p *JokerParams) ToConfig() (*joker.Config, error) {
	cnf := joker.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.APIMode, p.APIMode)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("joker: %w", err)
	}
	return cnf, nil
}
