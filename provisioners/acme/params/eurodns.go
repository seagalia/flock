package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/eurodns"
)

type EurodnsParams struct {
	ApplicationID      string `mapstructure:"application_id"`
	APIKey             string `mapstructure:"api_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *EurodnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return eurodns.NewDNSProviderConfig(cnf)
}

func (p *EurodnsParams) ToConfig() (*eurodns.Config, error) {
	cnf := eurodns.NewDefaultConfig()
	setString(&cnf.ApplicationID, p.ApplicationID)
	setString(&cnf.APIKey, p.APIKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("eurodns: %w", err)
	}
	return cnf, nil
}
