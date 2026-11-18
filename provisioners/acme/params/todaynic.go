package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/todaynic"
)

type TodaynicParams struct {
	AuthUserID         string `mapstructure:"auth_user_id"`
	APIKey             string `mapstructure:"api_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *TodaynicParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return todaynic.NewDNSProviderConfig(cnf)
}

func (p *TodaynicParams) ToConfig() (*todaynic.Config, error) {
	cnf := todaynic.NewDefaultConfig()
	setString(&cnf.AuthUserID, p.AuthUserID)
	setString(&cnf.APIKey, p.APIKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("todaynic: %w", err)
	}
	return cnf, nil
}
