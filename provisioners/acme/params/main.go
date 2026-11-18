package params

import (
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"

	"github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-viper/mapstructure/v2"
)

type FlockMainParam struct {
	Flock FlockParam `mapstructure:"flock"`
}

type FlockParam struct {
	AcmeParam AcmeParam `mapstructure:"acme"`
}

type AcmeParam struct {
	Abion            *AbionParams            `mapstructure:"abion"`
	Acmedns          *AcmednsParams          `mapstructure:"acmedns"`
	Active24         *Active24Params         `mapstructure:"active24"`
	Alidns           *AlidnsParams           `mapstructure:"alidns"`
	Aliesa           *AliesaParams           `mapstructure:"aliesa"`
	Allinkl          *AllinklParams          `mapstructure:"allinkl"`
	Alwaysdata       *AlwaysdataParams       `mapstructure:"alwaysdata"`
	Anexia           *AnexiaParams           `mapstructure:"anexia"`
	Artfiles         *ArtfilesParams         `mapstructure:"artfiles"`
	Arvancloud       *ArvancloudParams       `mapstructure:"arvancloud"`
	Auroradns        *AuroradnsParams        `mapstructure:"auroradns"`
	Autodns          *AutodnsParams          `mapstructure:"autodns"`
	Axelname         *AxelnameParams         `mapstructure:"axelname"`
	Azion            *AzionParams            `mapstructure:"azion"`
	Azuredns         *AzurednsParams         `mapstructure:"azuredns"`
	Baiducloud       *BaiducloudParams       `mapstructure:"baiducloud"`
	Beget            *BegetParams            `mapstructure:"beget"`
	Binarylane       *BinarylaneParams       `mapstructure:"binarylane"`
	Bindman          *BindmanParams          `mapstructure:"bindman"`
	Bluecat          *BluecatParams          `mapstructure:"bluecat"`
	Bluecatv2        *Bluecatv2Params        `mapstructure:"bluecatv2"`
	Bookmyname       *BookmynameParams       `mapstructure:"bookmyname"`
	Bunny            *BunnyParams            `mapstructure:"bunny"`
	Checkdomain      *CheckdomainParams      `mapstructure:"checkdomain"`
	Civo             *CivoParams             `mapstructure:"civo"`
	Clouddns         *ClouddnsParams         `mapstructure:"clouddns"`
	Cloudflare       *CloudflareParams       `mapstructure:"cloudflare"`
	Cloudns          *CloudnsParams          `mapstructure:"cloudns"`
	Cloudpress       *CloudpressParams       `mapstructure:"cloudpress"`
	Cloudru          *CloudruParams          `mapstructure:"cloudru"`
	Com35            *Com35Params            `mapstructure:"com35"`
	Connbyte         *ConnbyteParams         `mapstructure:"connbyte"`
	Conoha           *ConohaParams           `mapstructure:"conoha"`
	Conohav3         *Conohav3Params         `mapstructure:"conohav3"`
	Constellix       *ConstellixParams       `mapstructure:"constellix"`
	Corenetworks     *CorenetworksParams     `mapstructure:"corenetworks"`
	Cpanel           *CpanelParams           `mapstructure:"cpanel"`
	Curanet          *CuranetParams          `mapstructure:"curanet"`
	Czechia          *CzechiaParams          `mapstructure:"czechia"`
	Dandomain        *DandomainParams        `mapstructure:"dandomain"`
	Ddnss            *DdnssParams            `mapstructure:"ddnss"`
	Derak            *DerakParams            `mapstructure:"derak"`
	Desec            *DesecParams            `mapstructure:"desec"`
	Digitalocean     *DigitaloceanParams     `mapstructure:"digitalocean"`
	Dinahosting      *DinahostingParams      `mapstructure:"dinahosting"`
	Directadmin      *DirectadminParams      `mapstructure:"directadmin"`
	Dns51            *Dns51Params            `mapstructure:"dns51"`
	Dnscale          *DnscaleParams          `mapstructure:"dnscale"`
	Dnsexit          *DnsexitParams          `mapstructure:"dnsexit"`
	Dnshomede        *DnshomedeParams        `mapstructure:"dnshomede"`
	Dnsimple         *DnsimpleParams         `mapstructure:"dnsimple"`
	Dnsla            *DnslaParams            `mapstructure:"dnsla"`
	Dnsmadeeasy      *DnsmadeeasyParams      `mapstructure:"dnsmadeeasy"`
	Dnsmint          *DnsmintParams          `mapstructure:"dnsmint"`
	Dnsservices      *DnsservicesParams      `mapstructure:"dnsservices"`
	Dnsupdate        *DnsupdateParams        `mapstructure:"dnsupdate"`
	Dode             *DodeParams             `mapstructure:"dode"`
	Domeneshop       *DomeneshopParams       `mapstructure:"domeneshop"`
	Dreamhost        *DreamhostParams        `mapstructure:"dreamhost"`
	Duckdns          *DuckdnsParams          `mapstructure:"duckdns"`
	Dyn              *DynParams              `mapstructure:"dyn"`
	Dynadot          *DynadotParams          `mapstructure:"dynadot"`
	Dyndnsfree       *DyndnsfreeParams       `mapstructure:"dyndnsfree"`
	Dynu             *DynuParams             `mapstructure:"dynu"`
	Easydns          *EasydnsParams          `mapstructure:"easydns"`
	Edgecenter       *EdgecenterParams       `mapstructure:"edgecenter"`
	Edgedns          *EdgednsParams          `mapstructure:"edgedns"`
	Edgeone          *EdgeoneParams          `mapstructure:"edgeone"`
	Efficientip      *EfficientipParams      `mapstructure:"efficientip"`
	Epik             *EpikParams             `mapstructure:"epik"`
	Eurodns          *EurodnsParams          `mapstructure:"eurodns"`
	Euserv           *EuservParams           `mapstructure:"euserv"`
	Excedo           *ExcedoParams           `mapstructure:"excedo"`
	Exoscale         *ExoscaleParams         `mapstructure:"exoscale"`
	F5xc             *F5xcParams             `mapstructure:"f5xc"`
	Feno             *FenoParams             `mapstructure:"feno"`
	Fornex           *FornexParams           `mapstructure:"fornex"`
	Freemyip         *FreemyipParams         `mapstructure:"freemyip"`
	Gandi            *GandiParams            `mapstructure:"gandi"`
	Gandiv5          *Gandiv5Params          `mapstructure:"gandiv5"`
	Gcloud           *GcloudParams           `mapstructure:"gcloud"`
	Gcore            *GcoreParams            `mapstructure:"gcore"`
	Gehirn           *GehirnParams           `mapstructure:"gehirn"`
	Gigahostno       *GigahostnoParams       `mapstructure:"gigahostno"`
	Glesys           *GlesysParams           `mapstructure:"glesys"`
	Gname            *GnameParams            `mapstructure:"gname"`
	Godaddy          *GodaddyParams          `mapstructure:"godaddy"`
	Godaddyv3        *Godaddyv3Params        `mapstructure:"godaddyv3"`
	Gravity          *GravityParams          `mapstructure:"gravity"`
	Hetzner          *HetznerParams          `mapstructure:"hetzner"`
	Hostingde        *HostingdeParams        `mapstructure:"hostingde"`
	Hostinger        *HostingerParams        `mapstructure:"hostinger"`
	Hostingnl        *HostingnlParams        `mapstructure:"hostingnl"`
	Hosttech         *HosttechParams         `mapstructure:"hosttech"`
	Hostup           *HostupParams           `mapstructure:"hostup"`
	Httpnet          *HttpnetParams          `mapstructure:"httpnet"`
	Httpreq          *HttpreqParams          `mapstructure:"httpreq"`
	Huaweicloud      *HuaweicloudParams      `mapstructure:"huaweicloud"`
	Hurricane        *HurricaneParams        `mapstructure:"hurricane"`
	Hyperone         *HyperoneParams         `mapstructure:"hyperone"`
	Ibmcloud         *IbmcloudParams         `mapstructure:"ibmcloud"`
	Iijdpf           *IijdpfParams           `mapstructure:"iijdpf"`
	Infoblox         *InfobloxParams         `mapstructure:"infoblox"`
	Infomaniak       *InfomaniakParams       `mapstructure:"infomaniak"`
	Internetbs       *InternetbsParams       `mapstructure:"internetbs"`
	Inwx             *InwxParams             `mapstructure:"inwx"`
	Ionos            *IonosParams            `mapstructure:"ionos"`
	Ionoscloud       *IonoscloudParams       `mapstructure:"ionoscloud"`
	Ipv64            *Ipv64Params            `mapstructure:"ipv64"`
	Ispconfig        *IspconfigParams        `mapstructure:"ispconfig"`
	Ispconfigddns    *IspconfigddnsParams    `mapstructure:"ispconfigddns"`
	Jdcloud          *JdcloudParams          `mapstructure:"jdcloud"`
	Joker            *JokerParams            `mapstructure:"joker"`
	Katapult         *KatapultParams         `mapstructure:"katapult"`
	Keyhelp          *KeyhelpParams          `mapstructure:"keyhelp"`
	Leaseweb         *LeasewebParams         `mapstructure:"leaseweb"`
	Liara            *LiaraParams            `mapstructure:"liara"`
	Lightsail        *LightsailParams        `mapstructure:"lightsail"`
	Limacity         *LimacityParams         `mapstructure:"limacity"`
	Linode           *LinodeParams           `mapstructure:"linode"`
	Liquidweb        *LiquidwebParams        `mapstructure:"liquidweb"`
	Loopia           *LoopiaParams           `mapstructure:"loopia"`
	Luadns           *LuadnsParams           `mapstructure:"luadns"`
	Mailinabox       *MailinaboxParams       `mapstructure:"mailinabox"`
	Manageengine     *ManageengineParams     `mapstructure:"manageengine"`
	Metaname         *MetanameParams         `mapstructure:"metaname"`
	Metaregistrar    *MetaregistrarParams    `mapstructure:"metaregistrar"`
	Mijnhost         *MijnhostParams         `mapstructure:"mijnhost"`
	Mittwald         *MittwaldParams         `mapstructure:"mittwald"`
	Myaddr           *MyaddrParams           `mapstructure:"myaddr"`
	Mydnsjp          *MydnsjpParams          `mapstructure:"mydnsjp"`
	Myra             *MyraParams             `mapstructure:"myra"`
	Mythicbeasts     *MythicbeastsParams     `mapstructure:"mythicbeasts"`
	Namecheap        *NamecheapParams        `mapstructure:"namecheap"`
	Namedotcom       *NamedotcomParams       `mapstructure:"namedotcom"`
	Namesilo         *NamesiloParams         `mapstructure:"namesilo"`
	Namesurfer       *NamesurferParams       `mapstructure:"namesurfer"`
	Nearlyfreespeech *NearlyfreespeechParams `mapstructure:"nearlyfreespeech"`
	Nederhost        *NederhostParams        `mapstructure:"nederhost"`
	Neodigit         *NeodigitParams         `mapstructure:"neodigit"`
	Netcup           *NetcupParams           `mapstructure:"netcup"`
	Netcupv1         *Netcupv1Params         `mapstructure:"netcupv1"`
	Netlify          *NetlifyParams          `mapstructure:"netlify"`
	Netnod           *NetnodParams           `mapstructure:"netnod"`
	Nexdns           *NexdnsParams           `mapstructure:"nexdns"`
	Ngenix           *NgenixParams           `mapstructure:"ngenix"`
	Nicmanager       *NicmanagerParams       `mapstructure:"nicmanager"`
	Nicru            *NicruParams            `mapstructure:"nicru"`
	Nifcloud         *NifcloudParams         `mapstructure:"nifcloud"`
	Njalla           *NjallaParams           `mapstructure:"njalla"`
	Nodion           *NodionParams           `mapstructure:"nodion"`
	Nordname         *NordnameParams         `mapstructure:"nordname"`
	Ns1              *Ns1Params              `mapstructure:"ns1"`
	Octenium         *OcteniumParams         `mapstructure:"octenium"`
	Omglol           *OmglolParams           `mapstructure:"omglol"`
	Onecloudru       *OnecloudruParams       `mapstructure:"onecloudru"`
	Onlinenet        *OnlinenetParams        `mapstructure:"onlinenet"`
	Openprovider     *OpenproviderParams     `mapstructure:"openprovider"`
	Opusdns          *OpusdnsParams          `mapstructure:"opusdns"`
	Oraclecloud      *OraclecloudParams      `mapstructure:"oraclecloud"`
	Otc              *OtcParams              `mapstructure:"otc"`
	Ovh              *OvhParams              `mapstructure:"ovh"`
	Pdns             *PdnsParams             `mapstructure:"pdns"`
	Plesk            *PleskParams            `mapstructure:"plesk"`
	Pointdns         *PointdnsParams         `mapstructure:"pointdns"`
	Porkbun          *PorkbunParams          `mapstructure:"porkbun"`
	Poweradmin       *PoweradminParams       `mapstructure:"poweradmin"`
	Primedns         *PrimednsParams         `mapstructure:"primedns"`
	Rackspace        *RackspaceParams        `mapstructure:"rackspace"`
	Rage4            *Rage4Params            `mapstructure:"rage4"`
	Rainyun          *RainyunParams          `mapstructure:"rainyun"`
	Rcodezero        *RcodezeroParams        `mapstructure:"rcodezero"`
	Regfish          *RegfishParams          `mapstructure:"regfish"`
	Regru            *RegruParams            `mapstructure:"regru"`
	Rimuhosting      *RimuhostingParams      `mapstructure:"rimuhosting"`
	Route53          *Route53Params          `mapstructure:"route53"`
	Safedns          *SafednsParams          `mapstructure:"safedns"`
	Sakuracloud      *SakuracloudParams      `mapstructure:"sakuracloud"`
	Scaleway         *ScalewayParams         `mapstructure:"scaleway"`
	Scannet          *ScannetParams          `mapstructure:"scannet"`
	Selectel         *SelectelParams         `mapstructure:"selectel"`
	Selectelv2       *Selectelv2Params       `mapstructure:"selectelv2"`
	Selfhostde       *SelfhostdeParams       `mapstructure:"selfhostde"`
	Selfhostdev2     *Selfhostdev2Params     `mapstructure:"selfhostdev2"`
	Servercow        *ServercowParams        `mapstructure:"servercow"`
	Shellrent        *ShellrentParams        `mapstructure:"shellrent"`
	Simply           *SimplyParams           `mapstructure:"simply"`
	Sonic            *SonicParams            `mapstructure:"sonic"`
	Spaceship        *SpaceshipParams        `mapstructure:"spaceship"`
	Syse             *SyseParams             `mapstructure:"syse"`
	Technitium       *TechnitiumParams       `mapstructure:"technitium"`
	Tele3            *Tele3Params            `mapstructure:"tele3"`
	Tencentcloud     *TencentcloudParams     `mapstructure:"tencentcloud"`
	Timewebcloud     *TimewebcloudParams     `mapstructure:"timewebcloud"`
	Todaynic         *TodaynicParams         `mapstructure:"todaynic"`
	Transip          *TransipParams          `mapstructure:"transip"`
	Ucloud           *UcloudParams           `mapstructure:"ucloud"`
	Ultradns         *UltradnsParams         `mapstructure:"ultradns"`
	Uniteddomains    *UniteddomainsParams    `mapstructure:"uniteddomains"`
	Variomedia       *VariomediaParams       `mapstructure:"variomedia"`
	Veesp            *VeespParams            `mapstructure:"veesp"`
	Vegadns          *VegadnsParams          `mapstructure:"vegadns"`
	Vercel           *VercelParams           `mapstructure:"vercel"`
	Versio           *VersioParams           `mapstructure:"versio"`
	Vinyldns         *VinyldnsParams         `mapstructure:"vinyldns"`
	Virtualname      *VirtualnameParams      `mapstructure:"virtualname"`
	Vkcloud          *VkcloudParams          `mapstructure:"vkcloud"`
	Volcengine       *VolcengineParams       `mapstructure:"volcengine"`
	Vscale           *VscaleParams           `mapstructure:"vscale"`
	Vultr            *VultrParams            `mapstructure:"vultr"`
	Wannafind        *WannafindParams        `mapstructure:"wannafind"`
	Webglobe         *WebglobeParams         `mapstructure:"webglobe"`
	Webnamesca       *WebnamescaParams       `mapstructure:"webnamesca"`
	Webnamesru       *WebnamesruParams       `mapstructure:"webnamesru"`
	Websupport       *WebsupportParams       `mapstructure:"websupport"`
	Wedos            *WedosParams            `mapstructure:"wedos"`
	Westcn           *WestcnParams           `mapstructure:"westcn"`
	Xinnet           *XinnetParams           `mapstructure:"xinnet"`
	Yandex           *YandexParams           `mapstructure:"yandex"`
	Yandex360        *Yandex360Params        `mapstructure:"yandex360"`
	Yandexcloud      *YandexcloudParams      `mapstructure:"yandexcloud"`
	Zilore           *ZiloreParams           `mapstructure:"zilore"`
	Zoneedit         *ZoneeditParams         `mapstructure:"zoneedit"`
	Zoneee           *ZoneeeParams           `mapstructure:"zoneee"`
	Zonomi           *ZonomiParams           `mapstructure:"zonomi"`
}

type CertRequest struct {
	Email        string               `mapstructure:"flock.acme.email"`
	CADirURL     string               `mapstructure:"flock.acme.ca_dir_url"`
	KeyType      string               `mapstructure:"flock.acme.key_type"`
	Registration *RegistrationRequest `mapstructure:"flock.acme.registration"`
	KeyPem       *KeyPemRequest       `mapstructure:"flock.acme.key_pem"`
	DnsProvider  map[string]any       `mapstructure:",remain"`
}

func (r *CertRequest) GetEmail() string {
	return r.Email
}

func (r *CertRequest) GetRegistration() *acme.ExtendedAccount {
	if r.Registration == nil {
		return nil
	}
	return r.Registration.ExtendedAccount
}

func (r *CertRequest) GetPrivateKey() crypto.Signer {
	if r.KeyPem == nil {
		return nil
	}
	return r.KeyPem.Signer
}

func (r *CertRequest) UnmarshalMapstructure(input any) error {

	type plain CertRequest
	err := mapstructure.Decode(input, (*plain)(r))
	if err != nil {
		return err
	}
	if r.KeyType == "" {
		r.KeyType = string(certcrypto.EC256)
	} else {
		_, err = certcrypto.ToKeyType(r.KeyType)
		if err != nil {
			return fmt.Errorf("invalid key type: %s", r.KeyType)
		}
	}

	return nil
}

func (r *CertRequest) ToMap() (map[string]string, error) {
	reg, err := r.Registration.ToValue()
	if err != nil {
		return nil, err
	}
	keyPem, err := r.KeyPem.ToValue()
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"flock.acme.email":        r.Email,
		"flock.acme.ca_dir_url":   r.CADirURL,
		"flock.acme.key_type":     r.KeyType,
		"flock.acme.registration": reg,
		"flock.acme.key_pem":      keyPem,
	}, nil
}

func (r *CertRequest) ToMapSecretRef(secretUuid string) (map[string]string, error) {
	secretMap, err := r.ToSecretMap()
	if err != nil {
		return nil, err
	}
	for k := range secretMap {
		secretMap[k] = fmt.Sprintf("secret://%s/%s", secretUuid, k)
	}
	secretMap["flock.acme.email"] = r.Email
	secretMap["flock.acme.ca_dir_url"] = r.CADirURL
	secretMap["flock.acme.key_type"] = r.KeyType
	return secretMap, nil
}

func (r *CertRequest) ToSecretMap() (map[string]string, error) {
	m, err := r.ToMap()
	if err != nil {
		return nil, err
	}
	delete(m, "flock.acme.email")
	delete(m, "flock.acme.ca_dir_url")
	delete(m, "flock.acme.key_type")
	return m, nil
}

type RegistrationRequest struct {
	*acme.ExtendedAccount
}

func (r *RegistrationRequest) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	var account acme.ExtendedAccount
	err := json.Unmarshal([]byte(str), &account)
	if err != nil {
		return err
	}
	r.ExtendedAccount = &account
	return nil
}

func (r *RegistrationRequest) ToValue() (string, error) {
	jsonBytes, err := json.Marshal(r.ExtendedAccount)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}

type KeyPemRequest struct {
	crypto.Signer
}

func (r *KeyPemRequest) ToValue() (string, error) {
	der, err := x509.MarshalPKCS8PrivateKey(r.Signer)
	if err != nil {
		return "", fmt.Errorf("marshal private key: %w", err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	return string(data), nil
}

func (r *KeyPemRequest) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}
	block, _ := pem.Decode([]byte(str))
	if block == nil || block.Type != "PRIVATE KEY" {
		return errors.New("invalid PEM block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("parse private key: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return fmt.Errorf("unsupported key type %T", key)
	}
	r.Signer = signer
	return nil
}
