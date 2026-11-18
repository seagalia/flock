package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/bluecatv2"
)

type Bluecatv2Params struct {
	ServerURL          string `mapstructure:"server_url"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	ConfigName         string `mapstructure:"config_name"`
	ViewName           string `mapstructure:"view_name"`
	SkipDeploy         string `mapstructure:"skip_deploy"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *Bluecatv2Params) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return bluecatv2.NewDNSProviderConfig(cnf)
}

func (p *Bluecatv2Params) ToConfig() (*bluecatv2.Config, error) {
	cnf := bluecatv2.NewDefaultConfig()
	setString(&cnf.ServerURL, p.ServerURL)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.ConfigName, p.ConfigName)
	setString(&cnf.ViewName, p.ViewName)

	err := errors.Join(
		setBool(&cnf.SkipDeploy, "skip_deploy", p.SkipDeploy),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("bluecatv2: %w", err)
	}
	return cnf, nil
}
