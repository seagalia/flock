package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/ovh"
)

type OvhParams struct {
	APIEndpoint string `mapstructure:"api_endpoint"`

	ApplicationKey    string `mapstructure:"application_key"`
	ApplicationSecret string `mapstructure:"application_secret"`
	ConsumerKey       string `mapstructure:"consumer_key"`

	OAuth2Config *OAuth2Config `mapstructure:"oauth2"`

	AccessToken string `mapstructure:"access_token"`

	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (o *OvhParams) ToProvider() (challenge.Provider, error) {
	cnf, err := o.ToConfig()
	if err != nil {
		return nil, err
	}
	return ovh.NewDNSProviderConfig(cnf)
}

func (o *OvhParams) ToConfig() (*ovh.Config, error) {
	cnf := ovh.NewDefaultConfig()
	setString(&cnf.APIEndpoint, o.APIEndpoint)
	setString(&cnf.ApplicationKey, o.ApplicationKey)
	setString(&cnf.ApplicationSecret, o.ApplicationSecret)
	setString(&cnf.ConsumerKey, o.ConsumerKey)
	setString(&cnf.AccessToken, o.AccessToken)
	if o.OAuth2Config != nil {
		cnf.OAuth2Config = o.OAuth2Config.ToConfig()
	}

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", o.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", o.PollingInterval),
		setInt(&cnf.TTL, "ttl", o.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("ovh: %w", err)
	}
	return cnf, nil
}

type OAuth2Config struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
}

func (o *OAuth2Config) ToConfig() *ovh.OAuth2Config {
	return &ovh.OAuth2Config{
		ClientID:     o.ClientID,
		ClientSecret: o.ClientSecret,
	}
}
