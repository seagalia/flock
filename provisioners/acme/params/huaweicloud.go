package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/huaweicloud"
)

type HuaweicloudParams struct {
	AccessKeyID        string `mapstructure:"access_key_id"`
	SecretAccessKey    string `mapstructure:"secret_access_key"`
	Region             string `mapstructure:"region"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	HTTPTimeout        string `mapstructure:"http_timeout"`
}

func (p *HuaweicloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return huaweicloud.NewDNSProviderConfig(cnf)
}

func (p *HuaweicloudParams) ToConfig() (*huaweicloud.Config, error) {
	cnf := huaweicloud.NewDefaultConfig()
	setString(&cnf.AccessKeyID, p.AccessKeyID)
	setString(&cnf.SecretAccessKey, p.SecretAccessKey)
	setString(&cnf.Region, p.Region)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.HTTPTimeout, "http_timeout", p.HTTPTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("huaweicloud: %w", err)
	}
	return cnf, nil
}
