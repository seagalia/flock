package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/mailinabox"
)

type MailinaboxParams struct {
	Email              string `mapstructure:"email"`
	Password           string `mapstructure:"password"`
	BaseURL            string `mapstructure:"base_url"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *MailinaboxParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return mailinabox.NewDNSProviderConfig(cnf)
}

func (p *MailinaboxParams) ToConfig() (*mailinabox.Config, error) {
	cnf := mailinabox.NewDefaultConfig()
	setString(&cnf.Email, p.Email)
	setString(&cnf.Password, p.Password)
	setString(&cnf.BaseURL, p.BaseURL)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("mailinabox: %w", err)
	}
	return cnf, nil
}
