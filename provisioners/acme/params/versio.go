package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/versio"
)

type VersioParams struct {
	BaseURL            string `mapstructure:"base_url"`
	TTL                string `mapstructure:"ttl"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
}

func (p *VersioParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return versio.NewDNSProviderConfig(cnf)
}

func (p *VersioParams) ToConfig() (*versio.Config, error) {
	cnf := versio.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setURL(&cnf.BaseURL, "base_url", p.BaseURL),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("versio: %w", err)
	}
	return cnf, nil
}
