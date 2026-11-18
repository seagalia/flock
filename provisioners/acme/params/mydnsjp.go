package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/mydnsjp"
)

type MydnsjpParams struct {
	MasterID           string `mapstructure:"master_id"`
	Password           string `mapstructure:"password"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
}

func (p *MydnsjpParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return mydnsjp.NewDNSProviderConfig(cnf)
}

func (p *MydnsjpParams) ToConfig() (*mydnsjp.Config, error) {
	cnf := mydnsjp.NewDefaultConfig()
	setString(&cnf.MasterID, p.MasterID)
	setString(&cnf.Password, p.Password)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("mydnsjp: %w", err)
	}
	return cnf, nil
}
