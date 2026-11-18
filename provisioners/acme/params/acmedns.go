package params

import (
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/acmedns"
)

type AcmednsParams struct {
	APIBase        string `mapstructure:"api_base"`
	AllowList      string `mapstructure:"allow_list"`
	StoragePath    string `mapstructure:"storage_path"`
	StorageBaseURL string `mapstructure:"storage_base_url"`
}

func (p *AcmednsParams) ToProvider() (challenge.Provider, error) {
	cnf, err := p.ToConfig()
	if err != nil {
		return nil, err
	}
	return acmedns.NewDNSProviderConfig(cnf)
}

func (p *AcmednsParams) ToConfig() (*acmedns.Config, error) {
	cnf := acmedns.NewDefaultConfig()
	setString(&cnf.APIBase, p.APIBase)
	setString(&cnf.StoragePath, p.StoragePath)
	setString(&cnf.StorageBaseURL, p.StorageBaseURL)
	setList(&cnf.AllowList, p.AllowList)
	return cnf, nil
}
