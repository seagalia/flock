package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/httpreq"
)

type HttpreqParams struct {
	Endpoint           string `mapstructure:"endpoint"`
	Mode               string `mapstructure:"mode"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *HttpreqParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return httpreq.NewDNSProviderConfig(cnf)
}

func (p *HttpreqParams) ToConfig() (*httpreq.Config, error) {
	cnf := httpreq.NewDefaultConfig()
	setString(&cnf.Mode, p.Mode)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setURL(&cnf.Endpoint, "endpoint", p.Endpoint),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("httpreq: %w", err)
	}
	return cnf, nil
}
