package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/transip"
)

type TransipParams struct {
	AccountName        string `mapstructure:"account_name"`
	PrivateKeyPath     string `mapstructure:"private_key_path"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *TransipParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return transip.NewDNSProviderConfig(cnf)
}

func (p *TransipParams) ToConfig() (*transip.Config, error) {
	cnf := transip.NewDefaultConfig()
	setString(&cnf.AccountName, p.AccountName)
	setString(&cnf.PrivateKeyPath, p.PrivateKeyPath)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("transip: %w", err)
	}
	return cnf, nil
}
