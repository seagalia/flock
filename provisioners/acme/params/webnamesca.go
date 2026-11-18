package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/webnamesca"
)

type WebnamescaParams struct {
	APIUser            string `mapstructure:"api_user"`
	APIKey             string `mapstructure:"api_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *WebnamescaParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return webnamesca.NewDNSProviderConfig(cnf)
}

func (p *WebnamescaParams) ToConfig() (*webnamesca.Config, error) {
	cnf := webnamesca.NewDefaultConfig()
	setString(&cnf.APIUser, p.APIUser)
	setString(&cnf.APIKey, p.APIKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("webnamesca: %w", err)
	}
	return cnf, nil
}
