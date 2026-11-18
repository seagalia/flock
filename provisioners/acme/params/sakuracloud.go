package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/sakuracloud"
)

type SakuracloudParams struct {
	Token              string `mapstructure:"token"`
	Secret             string `mapstructure:"secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *SakuracloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return sakuracloud.NewDNSProviderConfig(cnf)
}

func (p *SakuracloudParams) ToConfig() (*sakuracloud.Config, error) {
	cnf := sakuracloud.NewDefaultConfig()
	setString(&cnf.Token, p.Token)
	setString(&cnf.Secret, p.Secret)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("sakuracloud: %w", err)
	}
	return cnf, nil
}
