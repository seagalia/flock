package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/edgedns"
)

// EdgednsParams holds the EdgeGrid credentials lego otherwise reads from ~/.edgerc or the AKAMAI_* variables.
type EdgednsParams struct {
	Host         string `mapstructure:"host"`
	ClientToken  string `mapstructure:"client_token"`
	ClientSecret string `mapstructure:"client_secret"`
	AccessToken  string `mapstructure:"access_token"`
	AccountKey   string `mapstructure:"account_key"`

	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *EdgednsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return edgedns.NewDNSProviderConfig(cnf)
}

func (p *EdgednsParams) ToConfig() (*edgedns.Config, error) {
	cnf := edgedns.NewDefaultConfig()
	setString(&cnf.Host, p.Host)
	setString(&cnf.ClientToken, p.ClientToken)
	setString(&cnf.ClientSecret, p.ClientSecret)
	setString(&cnf.AccessToken, p.AccessToken)
	setString(&cnf.AccountKey, p.AccountKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("edgedns: %w", err)
	}
	return cnf, nil
}
