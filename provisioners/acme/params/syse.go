package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/syse"
)

type SyseParams struct {
	Credentials        string `mapstructure:"credentials"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *SyseParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return syse.NewDNSProviderConfig(cnf)
}

func (p *SyseParams) ToConfig() (*syse.Config, error) {
	cnf := syse.NewDefaultConfig()

	err := errors.Join(
		setPairs(&cnf.Credentials, "credentials", p.Credentials),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("syse: %w", err)
	}
	return cnf, nil
}
