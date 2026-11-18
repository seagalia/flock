package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/tencentcloud"
)

type TencentcloudParams struct {
	SecretID           string `mapstructure:"secret_id"`
	SecretKey          string `mapstructure:"secret_key"`
	Region             string `mapstructure:"region"`
	SessionToken       string `mapstructure:"session_token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	HTTPTimeout        string `mapstructure:"http_timeout"`
}

func (p *TencentcloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return tencentcloud.NewDNSProviderConfig(cnf)
}

func (p *TencentcloudParams) ToConfig() (*tencentcloud.Config, error) {
	cnf := tencentcloud.NewDefaultConfig()
	setString(&cnf.SecretID, p.SecretID)
	setString(&cnf.SecretKey, p.SecretKey)
	setString(&cnf.Region, p.Region)
	setString(&cnf.SessionToken, p.SessionToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.HTTPTimeout, "http_timeout", p.HTTPTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("tencentcloud: %w", err)
	}
	return cnf, nil
}
