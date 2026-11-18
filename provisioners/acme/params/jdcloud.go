package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/jdcloud"
)

type JdcloudParams struct {
	AccessKeyID        string `mapstructure:"access_key_id"`
	AccessKeySecret    string `mapstructure:"access_key_secret"`
	RegionID           string `mapstructure:"region_id"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	HTTPTimeout        string `mapstructure:"http_timeout"`
}

func (p *JdcloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return jdcloud.NewDNSProviderConfig(cnf)
}

func (p *JdcloudParams) ToConfig() (*jdcloud.Config, error) {
	cnf := jdcloud.NewDefaultConfig()
	setString(&cnf.AccessKeyID, p.AccessKeyID)
	setString(&cnf.AccessKeySecret, p.AccessKeySecret)
	setString(&cnf.RegionID, p.RegionID)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.HTTPTimeout, "http_timeout", p.HTTPTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("jdcloud: %w", err)
	}
	return cnf, nil
}
