package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/gname"
)

type GnameParams struct {
	AppID              string `mapstructure:"app_id"`
	AppKey             string `mapstructure:"app_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *GnameParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return gname.NewDNSProviderConfig(cnf)
}

func (p *GnameParams) ToConfig() (*gname.Config, error) {
	cnf := gname.NewDefaultConfig()
	setString(&cnf.AppID, p.AppID)
	setString(&cnf.AppKey, p.AppKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("gname: %w", err)
	}
	return cnf, nil
}
