package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/iijdpf"
)

type IijdpfParams struct {
	Token              string `mapstructure:"token"`
	ServiceCode        string `mapstructure:"service_code"`
	Endpoint           string `mapstructure:"endpoint"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *IijdpfParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return iijdpf.NewDNSProviderConfig(cnf)
}

func (p *IijdpfParams) ToConfig() (*iijdpf.Config, error) {
	cnf := iijdpf.NewDefaultConfig()
	setString(&cnf.Token, p.Token)
	setString(&cnf.ServiceCode, p.ServiceCode)
	setString(&cnf.Endpoint, p.Endpoint)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("iijdpf: %w", err)
	}
	return cnf, nil
}
