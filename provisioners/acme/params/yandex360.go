package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/yandex360"
)

type Yandex360Params struct {
	OAuthToken         string `mapstructure:"oauth_token"`
	OrgID              string `mapstructure:"org_id"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *Yandex360Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return yandex360.NewDNSProviderConfig(cnf)
}

func (p *Yandex360Params) ToConfig() (*yandex360.Config, error) {
	cnf := yandex360.NewDefaultConfig()
	setString(&cnf.OAuthToken, p.OAuthToken)

	err := errors.Join(
		setInt(&cnf.OrgID, "org_id", p.OrgID),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("yandex360: %w", err)
	}
	return cnf, nil
}
