package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/nicmanager"
)

type NicmanagerParams struct {
	Login              string `mapstructure:"login"`
	Username           string `mapstructure:"username"`
	Email              string `mapstructure:"email"`
	Password           string `mapstructure:"password"`
	OTPSecret          string `mapstructure:"otp_secret"`
	Mode               string `mapstructure:"mode"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *NicmanagerParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return nicmanager.NewDNSProviderConfig(cnf)
}

func (p *NicmanagerParams) ToConfig() (*nicmanager.Config, error) {
	cnf := nicmanager.NewDefaultConfig()
	setString(&cnf.Login, p.Login)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Email, p.Email)
	setString(&cnf.Password, p.Password)
	setString(&cnf.OTPSecret, p.OTPSecret)
	setString(&cnf.Mode, p.Mode)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("nicmanager: %w", err)
	}
	return cnf, nil
}
