package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/gcloud"
)

type GcloudParams struct {
	Debug                     string `mapstructure:"debug"`
	Project                   string `mapstructure:"project"`
	ZoneID                    string `mapstructure:"zone_id"`
	AllowPrivateZone          string `mapstructure:"allow_private_zone"`
	ImpersonateServiceAccount string `mapstructure:"impersonate_service_account"`
	AccessToken               string `mapstructure:"access_token"`
	PropagationTimeout        string `mapstructure:"propagation_timeout"`
	PollingInterval           string `mapstructure:"polling_interval"`
	TTL                       string `mapstructure:"ttl"`
}

func (p *GcloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return gcloud.NewDNSProviderConfig(cnf)
}

func (p *GcloudParams) ToConfig() (*gcloud.Config, error) {
	cnf := gcloud.NewDefaultConfig()
	setString(&cnf.Project, p.Project)
	setString(&cnf.ZoneID, p.ZoneID)
	setString(&cnf.ImpersonateServiceAccount, p.ImpersonateServiceAccount)
	setString(&cnf.AccessToken, p.AccessToken)

	err := errors.Join(
		setBool(&cnf.Debug, "debug", p.Debug),
		setBool(&cnf.AllowPrivateZone, "allow_private_zone", p.AllowPrivateZone),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("gcloud: %w", err)
	}
	return cnf, nil
}
