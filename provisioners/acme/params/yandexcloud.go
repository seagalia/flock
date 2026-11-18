package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/yandexcloud"
)

type YandexcloudParams struct {
	IamToken           string `mapstructure:"iam_token"`
	FolderID           string `mapstructure:"folder_id"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *YandexcloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return yandexcloud.NewDNSProviderConfig(cnf)
}

func (p *YandexcloudParams) ToConfig() (*yandexcloud.Config, error) {
	cnf := yandexcloud.NewDefaultConfig()
	setString(&cnf.IamToken, p.IamToken)
	setString(&cnf.FolderID, p.FolderID)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("yandexcloud: %w", err)
	}
	return cnf, nil
}
