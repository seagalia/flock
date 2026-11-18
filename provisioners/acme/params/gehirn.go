package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/gehirn"
)

type GehirnParams struct {
	TokenID            string `mapstructure:"token_id"`
	TokenSecret        string `mapstructure:"token_secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *GehirnParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return gehirn.NewDNSProviderConfig(cnf)
}

func (p *GehirnParams) ToConfig() (*gehirn.Config, error) {
	cnf := gehirn.NewDefaultConfig()
	setString(&cnf.TokenID, p.TokenID)
	setString(&cnf.TokenSecret, p.TokenSecret)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("gehirn: %w", err)
	}
	return cnf, nil
}
