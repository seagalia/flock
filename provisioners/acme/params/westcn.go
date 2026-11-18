package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/westcn"
)

type WestcnParams struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *WestcnParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return westcn.NewDNSProviderConfig(cnf)
}

func (p *WestcnParams) ToConfig() (*westcn.Config, error) {
	cnf := westcn.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("westcn: %w", err)
	}
	return cnf, nil
}
