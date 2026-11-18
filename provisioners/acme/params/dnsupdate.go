package params

import (
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/dnsupdate"
)

type DnsupdateParams struct {
	Nameserver         string `mapstructure:"nameserver"`
	DNSTimeout         string `mapstructure:"dns_timeout"`
	Zones              string `mapstructure:"zones"`
	TSIGFile           string `mapstructure:"tsig_file"`
	TSIGAlgorithm      string `mapstructure:"tsig_algorithm"`
	TSIGKey            string `mapstructure:"tsig_key"`
	TSIGSecret         string `mapstructure:"tsig_secret"`
	TSIGGSSRealm       string `mapstructure:"tsig_gss_realm"`
	TSIGGSSUsername    string `mapstructure:"tsig_gss_username"`
	TSIGGSSPassword    string `mapstructure:"tsig_gss_password"`
	TSIGGSSKeytabFile  string `mapstructure:"tsig_gss_keytab_file"`
	PropagationTimeout string `mapstructure:"propagation_timeout"`
	PollingInterval    string `mapstructure:"polling_interval"`
	TTL                string `mapstructure:"ttl"`
	SequenceInterval   string `mapstructure:"sequence_interval"`
}

func (p *DnsupdateParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return dnsupdate.NewDNSProviderConfig(cnf)
}

func (p *DnsupdateParams) ToConfig() (*dnsupdate.Config, error) {
	cnf := dnsupdate.NewDefaultConfig()
	setString(&cnf.Nameserver, p.Nameserver)
	setString(&cnf.TSIGFile, p.TSIGFile)
	setString(&cnf.TSIGAlgorithm, p.TSIGAlgorithm)
	setString(&cnf.TSIGKey, p.TSIGKey)
	setString(&cnf.TSIGSecret, p.TSIGSecret)
	setString(&cnf.TSIGGSSRealm, p.TSIGGSSRealm)
	setString(&cnf.TSIGGSSUsername, p.TSIGGSSUsername)
	setString(&cnf.TSIGGSSPassword, p.TSIGGSSPassword)
	setString(&cnf.TSIGGSSKeytabFile, p.TSIGGSSKeytabFile)
	setList(&cnf.Zones, p.Zones)

	err := errors.Join(
		setDuration(&cnf.DNSTimeout, "dns_timeout", p.DNSTimeout),
		setDuration(&cnf.PropagationTimeout, "propagation_timeout", p.PropagationTimeout),
		setDuration(&cnf.PollingInterval, "polling_interval", p.PollingInterval),
		setInt(&cnf.TTL, "ttl", p.TTL),
		setDuration(&cnf.SequenceInterval, "sequence_interval", p.SequenceInterval),
	)
	if err != nil {
		return nil, fmt.Errorf("dnsupdate: %w", err)
	}
	return cnf, nil
}
