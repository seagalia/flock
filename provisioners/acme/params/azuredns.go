package params

import (
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/azuredns"
)

type AzurednsParams struct {
	ZoneName               string `mapstructure:"zone_name"`
	SubscriptionID         string `mapstructure:"subscription_id"`
	ResourceGroup          string `mapstructure:"resource_group"`
	PrivateZone            string `mapstructure:"private_zone"`
	Environment            string `mapstructure:"environment"`
	ClientID               string `mapstructure:"client_id"`
	ClientSecret           string `mapstructure:"client_secret"`
	TenantID               string `mapstructure:"tenant_id"`
	OIDCToken              string `mapstructure:"oidc_token"`
	OIDCTokenFilePath      string `mapstructure:"oidc_token_file_path"`
	OIDCRequestURL         string `mapstructure:"oidc_request_url"`
	OIDCRequestToken       string `mapstructure:"oidc_request_token"`
	ServiceConnectionID    string `mapstructure:"service_connection_id"`
	SystemAccessToken      string `mapstructure:"system_access_token"`
	AuthMethod             string `mapstructure:"auth_method"`
	AuthMSITimeout         string `mapstructure:"auth_msi_timeout"`
	PropagationTimeout     string `mapstructure:"propagation_timeout"`
	PollingInterval        string `mapstructure:"polling_interval"`
	TTL                    string `mapstructure:"ttl"`
	ServiceDiscoveryFilter string `mapstructure:"service_discovery_filter"`
}

func (p *AzurednsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return azuredns.NewDNSProviderConfig(cnf)
}

func (p *AzurednsParams) ToConfig() (*azuredns.Config, error) {
	cnf := azuredns.NewDefaultConfig()
	setString(&cnf.ZoneName, p.ZoneName)
	setString(&cnf.SubscriptionID, p.SubscriptionID)
	setString(&cnf.ResourceGroup, p.ResourceGroup)
	setString(&cnf.ClientID, p.ClientID)
	setString(&cnf.ClientSecret, p.ClientSecret)
	setString(&cnf.TenantID, p.TenantID)
	setString(&cnf.OIDCToken, p.OIDCToken)
	setString(&cnf.OIDCTokenFilePath, p.OIDCTokenFilePath)
	setString(&cnf.OIDCRequestURL, p.OIDCRequestURL)
	setString(&cnf.OIDCRequestToken, p.OIDCRequestToken)
	setString(&cnf.ServiceConnectionID, p.ServiceConnectionID)
	setString(&cnf.SystemAccessToken, p.SystemAccessToken)
	setString(&cnf.AuthMethod, p.AuthMethod)
	setString(&cnf.ServiceDiscoveryFilter, p.ServiceDiscoveryFilter)

	err := errors.Join(
		setAzureEnvironment(&cnf.Environment, p.Environment),
		setBool(&cnf.PrivateZone, "private_zone", p.PrivateZone),
		setDuration(&cnf.AuthMSITimeout, "auth_msi_timeout", p.AuthMSITimeout),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("azuredns: %w", err)
	}
	return cnf, nil
}

// setAzureEnvironment takes the environment names lego reads from AZURE_ENVIRONMENT.
func setAzureEnvironment(dst *cloud.Configuration, raw string) error {
	switch raw {
	case "":
		return nil
	case "public":
		*dst = cloud.AzurePublic
	case "china":
		*dst = cloud.AzureChina
	case "usgovernment":
		*dst = cloud.AzureGovernment
	default:
		return fmt.Errorf("unknown environment %q, expected public, china or usgovernment", raw)
	}
	return nil
}
