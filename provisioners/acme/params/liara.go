package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/liara"
)

type LiaraParams struct {
	APIKey             string `mapstructure:"api_key"`
	TeamID             string `mapstructure:"team_id"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *LiaraParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return liara.NewDNSProviderConfig(cnf)
}

func (p *LiaraParams) ToConfig() (*liara.Config, error) {
	cnf := liara.NewDefaultConfig()
	setString(&cnf.APIKey, p.APIKey)
	setString(&cnf.TeamID, p.TeamID)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("liara: %w", err)
	}
	return cnf, nil
}
