package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/nearlyfreespeech"
)

type NearlyfreespeechParams struct {
	APIKey             string `mapstructure:"api_key"`
	Login              string `mapstructure:"login"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
}

func (p *NearlyfreespeechParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return nearlyfreespeech.NewDNSProviderConfig(cnf)
}

func (p *NearlyfreespeechParams) ToConfig() (*nearlyfreespeech.Config, error) {
	cnf := nearlyfreespeech.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.Login, p.Login)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("nearlyfreespeech: %w", err)
	}
	return cnf, nil
}
