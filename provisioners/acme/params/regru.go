package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/regru"
)

type RegruParams struct {
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	TLSCert            string `mapstructure:"tls_cert"`
	TLSKey             string `mapstructure:"tls_key"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *RegruParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return regru.NewDNSProviderConfig(cnf)
}

func (p *RegruParams) ToConfig() (*regru.Config, error) {
	cnf := regru.NewDefaultConfig()
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.TLSCert, p.TLSCert)
	setString(&cnf.TLSKey, p.TLSKey)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("regru: %w", err)
	}
	return cnf, nil
}
