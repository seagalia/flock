package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/vercel"
)

type VercelParams struct {
	AuthToken          string `mapstructure:"auth_token"`
	TeamID             string `mapstructure:"team_id"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *VercelParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return vercel.NewDNSProviderConfig(cnf)
}

func (p *VercelParams) ToConfig() (*vercel.Config, error) {
	cnf := vercel.NewDefaultConfig()
	setString(&cnf.AuthToken, p.AuthToken)
	setString(&cnf.TeamID, p.TeamID)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("vercel: %w", err)
	}
	return cnf, nil
}
