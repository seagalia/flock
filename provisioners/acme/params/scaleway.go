package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/scaleway"
)

type ScalewayParams struct {
	ProjectID          string `mapstructure:"project_id"`
	SecretKey          string `mapstructure:"secret_key"`
	AccessKey          string `mapstructure:"access_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *ScalewayParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return scaleway.NewDNSProviderConfig(cnf)
}

func (p *ScalewayParams) ToConfig() (*scaleway.Config, error) {
	cnf := scaleway.NewDefaultConfig()
	setString(&cnf.ProjectID, p.ProjectID)
	setString(&cnf.SecretKey, p.SecretKey)
	setString(&cnf.AccessKey, p.AccessKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("scaleway: %w", err)
	}
	return cnf, nil
}
