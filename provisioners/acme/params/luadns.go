package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/luadns"
)

type LuadnsParams struct {
	APIUsername        string `mapstructure:"api_username"`
	APIToken           string `mapstructure:"api_token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *LuadnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return luadns.NewDNSProviderConfig(cnf)
}

func (p *LuadnsParams) ToConfig() (*luadns.Config, error) {
	cnf := luadns.NewDefaultConfig()
	setString(&cnf.APIUsername, p.APIUsername)
	setString(&cnf.APIToken, p.APIToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("luadns: %w", err)
	}
	return cnf, nil
}
