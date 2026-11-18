package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/route53"
)

type Route53Params struct {
	AccessKeyID              string `mapstructure:"access_key_id"`
	SecretAccessKey          string `mapstructure:"secret_access_key"`
	SessionToken             string `mapstructure:"session_token"`
	Region                   string `mapstructure:"region"`
	HostedZoneID             string `mapstructure:"hosted_zone_id"`
	MaxRetries               string `mapstructure:"max_retries"`
	AssumeRoleArn            string `mapstructure:"assume_role_arn"`
	ExternalID               string `mapstructure:"external_id"`
	PrivateZone              string `mapstructure:"private_zone"`
	WaitForRecordSetsChanged string `mapstructure:"wait_for_record_sets_changed"`
	TTL                      string `mapstructure:"ttl"`
	PropagationTimeout       string `mapstructure:"propagation_timeout"`
	PollingInterval          string `mapstructure:"polling_interval"`
}

func (p *Route53Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return route53.NewDNSProviderConfig(cnf)
}

func (p *Route53Params) ToConfig() (*route53.Config, error) {
	cnf := route53.NewDefaultConfig()
	setString(&cnf.AccessKeyID, p.AccessKeyID)
	setString(&cnf.SecretAccessKey, p.SecretAccessKey)
	setString(&cnf.SessionToken, p.SessionToken)
	setString(&cnf.Region, p.Region)
	setString(&cnf.HostedZoneID, p.HostedZoneID)
	setString(&cnf.AssumeRoleArn, p.AssumeRoleArn)
	setString(&cnf.ExternalID, p.ExternalID)

	err := errors.Join(
		setInt(&cnf.MaxRetries, "max_retries", p.MaxRetries),
		setBool(&cnf.PrivateZone, "private_zone", p.PrivateZone),
		setBool(&cnf.WaitForRecordSetsChanged, "wait_for_record_sets_changed", p.WaitForRecordSetsChanged),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("route53: %w", err)
	}
	return cnf, nil
}
