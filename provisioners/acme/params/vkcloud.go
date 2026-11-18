package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/vkcloud"
)

type VkcloudParams struct {
	ProjectID          string `mapstructure:"project_id"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	DNSEndpoint        string `mapstructure:"dns_endpoint"`
	IdentityEndpoint   string `mapstructure:"identity_endpoint"`
	DomainName         string `mapstructure:"domain_name"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *VkcloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return vkcloud.NewDNSProviderConfig(cnf)
}

func (p *VkcloudParams) ToConfig() (*vkcloud.Config, error) {
	cnf := vkcloud.NewDefaultConfig()
	setString(&cnf.ProjectID, p.ProjectID)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.DNSEndpoint, p.DNSEndpoint)
	setString(&cnf.IdentityEndpoint, p.IdentityEndpoint)
	setString(&cnf.DomainName, p.DomainName)

	err := errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("vkcloud: %w", err)
	}
	return cnf, nil
}
