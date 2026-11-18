package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/cloudru"
)

type CloudruParams struct {
	ServiceInstanceID  string `mapstructure:"service_instance_id"`
	KeyID              string `mapstructure:"key_id"`
	Secret             string `mapstructure:"secret"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *CloudruParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return cloudru.NewDNSProviderConfig(cnf)
}

func (p *CloudruParams) ToConfig() (*cloudru.Config, error) {
	cnf := cloudru.NewDefaultConfig()
	setString(&cnf.ServiceInstanceID, p.ServiceInstanceID)
	setString(&cnf.KeyID, p.KeyID)
	setString(&cnf.Secret, p.Secret)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("cloudru: %w", err)
	}
	return cnf, nil
}
