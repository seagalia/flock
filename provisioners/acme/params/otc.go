package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/otc"
)

type OtcParams struct {
	DomainName         string `mapstructure:"domain_name"`
	ProjectName        string `mapstructure:"project_name"`
	UserName           string `mapstructure:"user_name"`
	Password           string `mapstructure:"password"`
	IdentityEndpoint   string `mapstructure:"identity_endpoint"`
	PrivateZone        string `mapstructure:"private_zone"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *OtcParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return otc.NewDNSProviderConfig(cnf)
}

func (p *OtcParams) ToConfig() (*otc.Config, error) {
	cnf := otc.NewDefaultConfig()
	setString(&cnf.DomainName, p.DomainName)
	setString(&cnf.ProjectName, p.ProjectName)
	setString(&cnf.UserName, p.UserName)
	setString(&cnf.Password, p.Password)
	setString(&cnf.IdentityEndpoint, p.IdentityEndpoint)

	err := errors.Join(
		setBool(&cnf.PrivateZone, "private_zone", p.PrivateZone),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("otc: %w", err)
	}
	return cnf, nil
}
