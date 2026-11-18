package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/clouddns"
)

type ClouddnsParams struct {
	ClientID           string `mapstructure:"client_id"`
	Email              string `mapstructure:"email"`
	Password           string `mapstructure:"password"`
	TTL                string `mapstructure:"ttl"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *ClouddnsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return clouddns.NewDNSProviderConfig(cnf)
}

func (p *ClouddnsParams) ToConfig() (*clouddns.Config, error) {
	cnf := clouddns.NewDefaultConfig()
	setString(&cnf.ClientID, p.ClientID)
	setString(&cnf.Email, p.Email)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("clouddns: %w", err)
	}
	return cnf, nil
}
