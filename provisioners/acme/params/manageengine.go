package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/manageengine"
)

type ManageengineParams struct {
	ClientID           string `mapstructure:"client_id"`
	ClientSecret       string `mapstructure:"client_secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *ManageengineParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return manageengine.NewDNSProviderConfig(cnf)
}

func (p *ManageengineParams) ToConfig() (*manageengine.Config, error) {
	cnf := manageengine.NewDefaultConfig()
	setString(&cnf.ClientID, p.ClientID)
	setString(&cnf.ClientSecret, p.ClientSecret)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("manageengine: %w", err)
	}
	return cnf, nil
}
