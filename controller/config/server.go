package config

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/seagalia/flock/sdk/config"
)

type Server struct {
	JwtMaker    *config.JwtMaker `yaml:"jwt_maker"`
	Login       *ServerLogin     `yaml:"login"`
	Tls         *ServerTls       `yaml:"tls"`
	Dashboard   ServerDashboard  `yaml:"dashboard"`
	AdminConfig AdminConfig      `yaml:"admin"`
	ListenAddr  string           `yaml:"listen_addr"`
	// LoopbackHttpAddr serves the same routes over plain HTTP, for a browser on the same machine: it trusts no
	// self-signed certificate, but treats a loopback address as secure
	LoopbackHttpAddr string `yaml:"loopback_http_addr"`
}

func (u *Server) UnmarshalYAML(unmarshal func(any) error) error {
	type plain Server
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if u.Tls == nil {
		return fmt.Errorf("server config: tls is required")
	}
	if u.Login == nil {
		return fmt.Errorf("server config: login is required")
	}
	if u.JwtMaker == nil {
		return fmt.Errorf("server config: jwt_maker is required")
	}
	// tokens are routed on their issuer, a shared one would send provider tokens to the jwt_maker key
	isIssuerShared := u.Login.Oidc != nil && u.Login.Oidc.IssuerUrl == u.JwtMaker.Issuer
	if isIssuerShared {
		return fmt.Errorf("server config: oidc issuer_url must differ from jwt_maker.issuer")
	}
	if u.ListenAddr == "" {
		u.ListenAddr = ":8080"
	}
	if u.LoopbackHttpAddr != "" && !isLoopbackAddr(u.LoopbackHttpAddr) {
		return fmt.Errorf("server config: loopback_http_addr %q must be on a loopback address, such as 127.0.0.1:8480", u.LoopbackHttpAddr)
	}
	return nil
}

// isLoopbackAddr tells whether a listen address only accepts connections from the machine itself, an empty host
// listens on every interface
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type ServerTls struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CaFile   string `yaml:"ca_file"`
}

func (u *ServerTls) UnmarshalYAML(unmarshal func(any) error) error {
	type plain ServerTls
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if u.CertFile == "" || u.KeyFile == "" {
		return fmt.Errorf("server tls: cert_file and key_file are required")
	}
	return nil
}

type ServerLogin struct {
	LockoutAfterFailures int               `yaml:"lockout_after_failures"`
	LockoutDuration      config.Duration   `yaml:"lockout_duration"`
	Type                 string            `yaml:"type"`
	BasicAuth            []*BasicAuthLogin `yaml:"basic_auth_options"`
	Ldap                 *LdapLogin        `yaml:"ldap_options"`
	Oidc                 *OidcLogin        `yaml:"oidc_options"`
}

func (u *ServerLogin) UnmarshalYAML(unmarshal func(any) error) error {
	type plain ServerLogin
	err := unmarshal((*plain)(u))
	if err != nil {
		return err
	}
	if u.Type == "" {
		return fmt.Errorf("server login: type is required")
	}
	u.Type = strings.ToLower(u.Type)
	err = u.checkOptions()
	if err != nil {
		return err
	}
	if u.LockoutAfterFailures <= 0 {
		u.LockoutAfterFailures = 5
	}
	if u.LockoutDuration <= 0 {
		u.LockoutDuration = config.Duration(5 * time.Minute)
	}
	return nil
}

// checkOptions makes sure the options of the type are set and no other, a forgotten block would be silently ignored
func (u *ServerLogin) checkOptions() error {
	hasBasic := len(u.BasicAuth) > 0
	hasLdap := u.Ldap != nil
	hasOidc := u.Oidc != nil
	switch u.Type {
	case "basic":
		if !hasBasic {
			return fmt.Errorf("server login: basic_auth_options needs at least one user")
		}
	case "ldap":
		if !hasLdap {
			return fmt.Errorf("server login: ldap_options is required")
		}
	case "oidc":
		if !hasOidc {
			return fmt.Errorf("server login: oidc_options is required")
		}
	default:
		return fmt.Errorf("server login: unknown type %s, valid types are basic, ldap and oidc", u.Type)
	}
	if hasBasic && u.Type != "basic" {
		return fmt.Errorf("server login: basic_auth_options is set but type is %s", u.Type)
	}
	if hasLdap && u.Type != "ldap" {
		return fmt.Errorf("server login: ldap_options is set but type is %s", u.Type)
	}
	if hasOidc && u.Type != "oidc" {
		return fmt.Errorf("server login: oidc_options is set but type is %s", u.Type)
	}
	return nil
}

// ServerDashboard serves the web dashboard at /dashboard, users log in with the login service of the controller
type ServerDashboard struct {
	Enabled bool `yaml:"enabled"`
}
