package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/efficientip"
)

type EfficientipParams struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	Hostname           string `mapstructure:"hostname"`
	DNSName            string `mapstructure:"dns_name"`
	ViewName           string `mapstructure:"view_name"`
	InsecureSkipVerify string `mapstructure:"insecure_skip_verify"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *EfficientipParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return efficientip.NewDNSProviderConfig(cnf)
}

func (p *EfficientipParams) ToConfig() (*efficientip.Config, error) {
	cnf := efficientip.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.Hostname, p.Hostname)
	setString(&cnf.DNSName, p.DNSName)
	setString(&cnf.ViewName, p.ViewName)

	err := errors.Join(
		setBool(&cnf.InsecureSkipVerify, "insecure_skip_verify", p.InsecureSkipVerify),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("efficientip: %w", err)
	}
	return cnf, nil
}
