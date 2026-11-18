package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/ucloud"
)

type UcloudParams struct {
	Region             string `mapstructure:"region"`
	ProjectID          string `mapstructure:"project_id"`
	PublicKey          string `mapstructure:"public_key"`
	PrivateKey         string `mapstructure:"private_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	HTTPTimeout        string `mapstructure:"http_timeout"`
}

func (p *UcloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return ucloud.NewDNSProviderConfig(cnf)
}

func (p *UcloudParams) ToConfig() (*ucloud.Config, error) {
	cnf := ucloud.NewDefaultConfig()
	setString(&cnf.Region, p.Region)
	setString(&cnf.ProjectID, p.ProjectID)
	setString(&cnf.PublicKey, p.PublicKey)
	setString(&cnf.PrivateKey, p.PrivateKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.HTTPTimeout, "http_timeout", p.HTTPTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("ucloud: %w", err)
	}
	return cnf, nil
}
