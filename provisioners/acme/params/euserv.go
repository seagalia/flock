package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/euserv"
)

type EuservParams struct {
	Email              string `mapstructure:"email"`
	Password           string `mapstructure:"password"`
	OrderID            string `mapstructure:"order_id"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *EuservParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return euserv.NewDNSProviderConfig(cnf)
}

func (p *EuservParams) ToConfig() (*euserv.Config, error) {
	cnf := euserv.NewDefaultConfig()
	setString(&cnf.Email, p.Email)
	setString(&cnf.Password, p.Password)
	setString(&cnf.OrderID, p.OrderID)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("euserv: %w", err)
	}
	return cnf, nil
}
