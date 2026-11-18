package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/ngenix"
)

type NgenixParams struct {
	Username           string `mapstructure:"username"`
	Token              string `mapstructure:"token"`
	CustomerID         string `mapstructure:"customer_id"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *NgenixParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return ngenix.NewDNSProviderConfig(cnf)
}

func (p *NgenixParams) ToConfig() (*ngenix.Config, error) {
	cnf := ngenix.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Token, p.Token)
	setString(&cnf.CustomerID, p.CustomerID)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("ngenix: %w", err)
	}
	return cnf, nil
}
