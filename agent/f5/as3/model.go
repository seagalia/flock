// Package as3 turns the flock resources of one BIG-IP partition into an AS3 declaration. It does no I/O: the same
// input always gives the same declaration, byte for byte, which is what makes the agent idempotent.
package as3

import (
	"bytes"
	"encoding/json"
	"maps"
)

// marshal is json.Marshal without the HTML escaping of <, > and &, which iControl REST misreads in iRule TCL.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// The structs below are the AS3 classes the agent declares, with only the properties it sets. AS3 references an
// object of the same application by its name, an existing BIG-IP object with BigipRef, and some properties need
// UseRef to point to an object of the application.

type BigipRef struct {
	Bigip string `json:"bigip"`
}

type UseRef struct {
	Use string `json:"use"`
}

// Tenant is an AS3 tenant, a BIG-IP partition, holding one application per VIP.
type Tenant struct {
	Remark string
	Apps   map[string]*Application
}

func (t Tenant) MarshalJSON() ([]byte, error) {
	obj := map[string]any{"class": classTenant}
	if t.Remark != "" {
		obj["remark"] = t.Remark
	}
	for name, app := range t.Apps {
		obj[name] = app
	}
	return marshal(obj)
}

type Application struct {
	Remark  string
	Objects map[string]any
}

func (a *Application) MarshalJSON() ([]byte, error) {
	obj := map[string]any{"class": classApplication}
	if a.Remark != "" {
		obj["remark"] = a.Remark
	}
	maps.Copy(obj, a.Objects)
	return marshal(obj)
}

type Service struct {
	Class  string `json:"class"`
	Remark string `json:"remark,omitempty"`
	// VirtualType "internal" is a virtual only reached by name from an iRule, with no address nor port.
	VirtualType      string   `json:"virtualType,omitempty"`
	VirtualAddresses []string `json:"virtualAddresses,omitempty"`
	VirtualPort      uint32   `json:"virtualPort,omitempty"`
	Pool             string   `json:"pool,omitempty"`
	Snat             any      `json:"snat,omitempty"`
	// PersistenceMethods is always written: AS3 adds cookie or source-address persistence when it is absent.
	PersistenceMethods        []any  `json:"persistenceMethods"`
	FallbackPersistenceMethod any    `json:"fallbackPersistenceMethod,omitempty"`
	IRules                    []any  `json:"iRules,omitempty"`
	ServerTLS                 any    `json:"serverTLS,omitempty"`
	ClientTLS                 any    `json:"clientTLS,omitempty"`
	PolicyWAF                 any    `json:"policyWAF,omitempty"`
	ProfileHTTP               any    `json:"profileHTTP,omitempty"`
	ProfileTCP                any    `json:"profileTCP,omitempty"`
	ProfileUDP                any    `json:"profileUDP,omitempty"`
	MaxConnections            uint64 `json:"maxConnections,omitempty"`
	RateLimit                 uint64 `json:"rateLimit,omitempty"`
	Redirect80                *bool  `json:"redirect80,omitempty"`
}

type Pool struct {
	Class             string   `json:"class"`
	Remark            string   `json:"remark,omitempty"`
	LoadBalancingMode string   `json:"loadBalancingMode"`
	SlowRampTime      uint64   `json:"slowRampTime,omitempty"`
	Monitors          []any    `json:"monitors,omitempty"`
	Members           []Member `json:"members"`
}

// Member is a set of static addresses, or a DNS name the BIG-IP resolves itself when AddressDiscovery is "fqdn".
type Member struct {
	ServicePort      uint32   `json:"servicePort"`
	ServerAddresses  []string `json:"serverAddresses,omitempty"`
	AddressDiscovery string   `json:"addressDiscovery,omitempty"`
	Hostname         string   `json:"hostname,omitempty"`
	AutoPopulate     bool     `json:"autoPopulate,omitempty"`
	AddressFamily    string   `json:"addressFamily,omitempty"`
	QueryInterval    uint64   `json:"queryInterval,omitempty"`
	Ratio            uint32   `json:"ratio,omitempty"`
	ShareNodes       bool     `json:"shareNodes,omitempty"`
}

type Monitor struct {
	Class       string `json:"class"`
	Remark      string `json:"remark,omitempty"`
	MonitorType string `json:"monitorType"`
	Interval    uint64 `json:"interval"`
	Timeout     uint64 `json:"timeout"`
	TimeUntilUp uint64 `json:"timeUntilUp,omitempty"`
	TargetPort  uint32 `json:"targetPort,omitempty"`
	Send        string `json:"send,omitempty"`
	Receive     string `json:"receive,omitempty"`
}

type IRule struct {
	Class  string `json:"class"`
	Remark string `json:"remark,omitempty"`
	IRule  string `json:"iRule"`
}

type Certificate struct {
	Class       string `json:"class"`
	Remark      string `json:"remark,omitempty"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"privateKey"`
	ChainCA     string `json:"chainCA,omitempty"`
}

type CABundle struct {
	Class  string `json:"class"`
	Bundle string `json:"bundle"`
}

type TLSServer struct {
	Class        string          `json:"class"`
	Certificates []TLSServerCert `json:"certificates"`
}

type TLSServerCert struct {
	Certificate string `json:"certificate"`
	MatchToSNI  string `json:"matchToSNI,omitempty"`
}

type TLSClient struct {
	Class               string `json:"class"`
	ServerName          string `json:"serverName,omitempty"`
	ValidateCertificate bool   `json:"validateCertificate,omitempty"`
	TrustCA             any    `json:"trustCA,omitempty"`
}
