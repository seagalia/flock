package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/axelname"
)

type AxelnameParams struct {
	Nickname           string `mapstructure:"nickname"`
	Token              string `mapstructure:"token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *AxelnameParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return axelname.NewDNSProviderConfig(cnf)
}

func (p *AxelnameParams) ToConfig() (*axelname.Config, error) {
	cnf := axelname.NewDefaultConfig()
	setString(&cnf.Nickname, p.Nickname)
	setString(&cnf.Token, p.Token)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("axelname: %w", err)
	}
	return cnf, nil
}
