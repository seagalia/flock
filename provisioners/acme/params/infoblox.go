package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/infoblox"
)

type InfobloxParams struct {
	Host               string `mapstructure:"host"`
	Port               string `mapstructure:"port"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	DNSView            string `mapstructure:"dns_view"`
	WapiVersion        string `mapstructure:"wapi_version"`
	SSLVerify          string `mapstructure:"ssl_verify"`
	CACertificate      string `mapstructure:"ca_certificate"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	HTTPTimeout        string `mapstructure:"http_timeout"`
}

func (p *InfobloxParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return infoblox.NewDNSProviderConfig(cnf)
}

func (p *InfobloxParams) ToConfig() (*infoblox.Config, error) {
	cnf := infoblox.NewDefaultConfig()
	setString(&cnf.Host, p.Host)
	setString(&cnf.Port, p.Port)
	setString(&cnf.Username, p.Username)
	setString(&cnf.Password, p.Password)
	setString(&cnf.DNSView, p.DNSView)
	setString(&cnf.WapiVersion, p.WapiVersion)
	setString(&cnf.CACertificate, p.CACertificate)

	err := errors.Join(
		setBool(&cnf.SSLVerify, "ssl_verify", p.SSLVerify),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setInt(&cnf.HTTPTimeout, "http_timeout", p.HTTPTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("infoblox: %w", err)
	}
	return cnf, nil
}
