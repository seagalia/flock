package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/yandex"
)

type YandexParams struct {
	PddToken           string `mapstructure:"pdd_token"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *YandexParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return yandex.NewDNSProviderConfig(cnf)
}

func (p *YandexParams) ToConfig() (*yandex.Config, error) {
	cnf := yandex.NewDefaultConfig()
	setString(&cnf.PddToken, p.PddToken)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("yandex: %w", err)
	}
	return cnf, nil
}
