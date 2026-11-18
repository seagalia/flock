package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/oraclecloud"
	"github.com/nrdcg/oci-go-sdk/common/v1065"
	"github.com/nrdcg/oci-go-sdk/common/v1065/auth"
)

const oracleInstancePrincipal = "instance_principal"

// OraclecloudParams signs the OCI calls with an API key, or with the instance principal of the host when auth_type
// is instance_principal (then only compartment_ocid and region are read).
type OraclecloudParams struct {
	AuthType             string `mapstructure:"auth_type"`
	CompartmentOCID      string `mapstructure:"compartment_ocid"`
	Region               string `mapstructure:"region"`
	TenancyOCID          string `mapstructure:"tenancy_ocid"`
	UserOCID             string `mapstructure:"user_ocid"`
	PubKeyFingerprint    string `mapstructure:"pubkey_fingerprint"`
	PrivateKey           string `mapstructure:"private_key"`
	PrivateKeyPassphrase string `mapstructure:"private_key_passphrase"`

	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
}

func (p *OraclecloudParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return oraclecloud.NewDNSProviderConfig(cnf)
}

func (p *OraclecloudParams) ToConfig() (*oraclecloud.Config, error) {
	cnf := oraclecloud.NewDefaultConfig()
	cnf.CompartmentID = p.CompartmentOCID

	configProvider, err := p.configurationProvider()
	if err != nil {
		return nil, fmt.Errorf("oraclecloud: %w", err)
	}
	cnf.OCIConfigProvider = configProvider

	err = errors.Join(
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
	)
	if err != nil {
		return nil, fmt.Errorf("oraclecloud: %w", err)
	}
	return cnf, nil
}

func (p *OraclecloudParams) configurationProvider() (common.ConfigurationProvider, error) {
	switch p.AuthType {
	case oracleInstancePrincipal:
		return auth.InstancePrincipalConfigurationProviderForRegion(common.Region(p.Region))
	case "":
		var passphrase *string
		if p.PrivateKeyPassphrase != "" {
			passphrase = &p.PrivateKeyPassphrase
		}
		return common.NewRawConfigurationProvider(
			p.TenancyOCID, p.UserOCID, p.Region, p.PubKeyFingerprint, p.PrivateKey, passphrase,
		), nil
	default:
		return nil, fmt.Errorf("unknown auth_type %q, expected empty or %q", p.AuthType, oracleInstancePrincipal)
	}
}
