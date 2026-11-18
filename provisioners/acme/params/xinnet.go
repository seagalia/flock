package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/xinnet"
)

type XinnetParams struct {
	Secret             string `mapstructure:"secret"`
	AgentID            string `mapstructure:"agent_id"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *XinnetParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return xinnet.NewDNSProviderConfig(cnf)
}

func (p *XinnetParams) ToConfig() (*xinnet.Config, error) {
	cnf := xinnet.NewDefaultConfig()
	setString(&cnf.Secret, p.Secret)
	setString(&cnf.AgentID, p.AgentID)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("xinnet: %w", err)
	}
	return cnf, nil
}
