package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/bluecat"
)

type BluecatParams struct {
	BaseURL            string `mapstructure:"base_url"`
	UserName           string `mapstructure:"user_name"`
	Password           string `mapstructure:"password"`
	ConfigName         string `mapstructure:"config_name"`
	DNSView            string `mapstructure:"dns_view"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	Debug              string `mapstructure:"debug"`
	SkipDeploy         string `mapstructure:"skip_deploy"`
}

func (p *BluecatParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return bluecat.NewDNSProviderConfig(cnf)
}

func (p *BluecatParams) ToConfig() (*bluecat.Config, error) {
	cnf := bluecat.NewDefaultConfig()
	setString(&cnf.BaseURL, p.BaseURL)
	setString(&cnf.UserName, p.UserName)
	setString(&cnf.Password, p.Password)
	setString(&cnf.ConfigName, p.ConfigName)
	setString(&cnf.DNSView, p.DNSView)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setBool(&cnf.Debug, "debug", p.Debug),
		setBool(&cnf.SkipDeploy, "skip_deploy", p.SkipDeploy),
	)
	if err != nil {
		return nil, fmt.Errorf("bluecat: %w", err)
	}
	return cnf, nil
}
