package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/artfiles"
)

type ArtfilesParams struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	ServerName         string `mapstructure:"server_name"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *ArtfilesParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return artfiles.NewDNSProviderConfig(cnf)
}

func (p *ArtfilesParams) ToConfig() (*artfiles.Config, error) {
	cnf := artfiles.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.ServerName, p.ServerName)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("artfiles: %w", err)
	}
	return cnf, nil
}
