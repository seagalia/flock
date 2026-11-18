package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/zilore"
)

type ZiloreParams struct {
	AccessKey          string `mapstructure:"access_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *ZiloreParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return zilore.NewDNSProviderConfig(cnf)
}

func (p *ZiloreParams) ToConfig() (*zilore.Config, error) {
	cnf := zilore.NewDefaultConfig()
	setString(&cnf.AccessKey, p.AccessKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("zilore: %w", err)
	}
	return cnf, nil
}
