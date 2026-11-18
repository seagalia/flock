package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/baiducloud"
)

type BaiducloudParams struct {
	AccessKeyID        string `mapstructure:"access_key_id"`
	SecretAccessKey    string `mapstructure:"secret_access_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *BaiducloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return baiducloud.NewDNSProviderConfig(cnf)
}

func (p *BaiducloudParams) ToConfig() (*baiducloud.Config, error) {
	cnf := baiducloud.NewDefaultConfig()
	setString(&cnf.AccessKeyID, p.AccessKeyID)
	setString(&cnf.SecretAccessKey, p.SecretAccessKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("baiducloud: %w", err)
	}
	return cnf, nil
}
