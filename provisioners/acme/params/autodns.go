package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/autodns"
)

type AutodnsParams struct {
	Endpoint           string `mapstructure:"endpoint"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	Context            string `mapstructure:"context"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *AutodnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return autodns.NewDNSProviderConfig(cnf)
}

func (p *AutodnsParams) ToConfig() (*autodns.Config, error) {
	cnf := autodns.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setURL(&cnf.Endpoint, "endpoint", p.Endpoint),
		setInt(&cnf.Context, "context", p.Context),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("autodns: %w", err)
	}
	return cnf, nil
}
