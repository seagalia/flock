package config

import (
	"fmt"
	"net"
	"net/url"
	"slices"

	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
)

type LdapLogin struct {
	// Search user bind dn
	BindDn string `yaml:"bind_dn"`
	// Search user bind password
	BindPassword string `yaml:"bind_password"`
	// Ldap server address in the form of host:port
	Address string `yaml:"address"`
	// Set to true if ldap server supports TLS
	UseSsl bool `yaml:"use_ssl"`
	// Set to true to skip certificate check (NOT RECOMMENDED)
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	// base dns to search through (Default: `dc=com`)
	SearchBaseDns string `yaml:"search_base_dns"`
	// Username search filter, for example "(cn=%s)" or "(sAMAccountName=%s)" or "(uid=%s)" (default: `(objectClass=organizationalPerson)&(uid=%s)`)
	SearchFilter string `yaml:"search_filter"`
	// Group search filter, to retrieve the groups of which the user is a member
	// if GroupSearchFilter or GroupSearchBaseDns or GroupAttribute are empty it will not search for groups
	// fist value given is user dn, example of value: `(member=%s)` will become `(member=cn=user,ou=users,dc=com)`
	GroupSearchFilter string `yaml:"group_search_filter"`
	// base DNs to search through for groups
	GroupSearchBaseDns string `yaml:"group_search_base_dns"`
	// Get group name by this attribute (default: `cn`)
	GroupAttribute string `yaml:"group_attribute"`
}

func (l *LdapLogin) UnmarshalYAML(unmarshal func(any) error) error {
	type plain LdapLogin
	err := unmarshal((*plain)(l))
	if err != nil {
		return err
	}
	if l.BindDn == "" {
		return fmt.Errorf("ldap login: bind_dn is required")
	}
	if l.BindPassword == "" {
		return fmt.Errorf("ldap login: bind_password is required")
	}
	if l.Address == "" {
		return fmt.Errorf("ldap login: address is required")
	}
	if l.SearchBaseDns == "" {
		l.SearchBaseDns = "dc=com"
	}
	if l.SearchFilter == "" {
		l.SearchFilter = "(objectClass=organizationalPerson)&(uid=%s)"
	}
	if l.GroupAttribute == "" {
		l.GroupAttribute = "cn"
	}
	return nil
}

type BasicAuthLogin struct {
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
	Groups   []string `yaml:"groups"`
}

func (l *BasicAuthLogin) UnmarshalYAML(unmarshal func(any) error) error {
	type plain BasicAuthLogin
	err := unmarshal((*plain)(l))
	if err != nil {
		return err
	}
	if l.Username == "" {
		return fmt.Errorf("basic auth login: user is required")
	}
	if l.Username == svclogin.BreakGlassUsername {
		return fmt.Errorf("basic auth login: username %s is reserved for break-glass tokens", svclogin.BreakGlassUsername)
	}
	if l.Password == "" {
		return fmt.Errorf("basic auth login: password is required")
	}
	return nil
}

// OidcLogin verifies the tokens of an OpenID Connect provider, users and machines get them from the provider directly
type OidcLogin struct {
	// Issuer of the provider, discovery is done at <issuer_url>/.well-known/openid-configuration
	IssuerUrl string `yaml:"issuer_url"`
	// Public client of the dashboard and the cli at the provider, ID tokens must be issued for it
	ClientId string `yaml:"client_id"`
	// Scopes clients ask for, openid is always added (default: `openid`, `profile`, `email`)
	Scopes []string `yaml:"scopes"`
	// Claim of ID tokens holding the username (default: `preferred_username`)
	UsernameClaim string `yaml:"username_claim"`
	// Claim of tokens holding the groups (default: `groups`)
	GroupsClaim string `yaml:"groups_claim"`
	// Set it to accept JWT access tokens, given to machines with client credentials
	AccessToken *OidcAccessToken `yaml:"access_token"`
	// Accept an http issuer_url on another host than the loopback, for test providers only
	InsecureHttp bool `yaml:"insecure_http"`
}

func (l *OidcLogin) UnmarshalYAML(unmarshal func(any) error) error {
	type plain OidcLogin
	err := unmarshal((*plain)(l))
	if err != nil {
		return err
	}
	if l.IssuerUrl == "" {
		return fmt.Errorf("oidc login: issuer_url is required")
	}
	err = l.checkIssuerUrl()
	if err != nil {
		return err
	}
	if l.ClientId == "" {
		return fmt.Errorf("oidc login: client_id is required")
	}
	if len(l.Scopes) == 0 {
		l.Scopes = []string{"openid", "profile", "email"}
	}
	if !slices.Contains(l.Scopes, "openid") {
		l.Scopes = append([]string{"openid"}, l.Scopes...)
	}
	if l.UsernameClaim == "" {
		l.UsernameClaim = "preferred_username"
	}
	if l.GroupsClaim == "" {
		l.GroupsClaim = "groups"
	}
	return nil
}

// checkIssuerUrl refuses an issuer_url that is not an absolute url, and plain http outside the loopback: the cli
// sends refresh tokens and client secrets to the endpoints it discovers there.
func (l *OidcLogin) checkIssuerUrl() error {
	issuer, err := url.Parse(l.IssuerUrl)
	isHttpScheme := err == nil && (issuer.Scheme == "https" || issuer.Scheme == "http")
	if !isHttpScheme || issuer.Host == "" {
		return fmt.Errorf("oidc login: issuer_url %q must be an absolute https url", l.IssuerUrl)
	}
	hostname := issuer.Hostname()
	isLoopback := hostname == "localhost" || net.ParseIP(hostname).IsLoopback()
	isPlainHttpRefused := issuer.Scheme == "http" && !isLoopback && !l.InsecureHttp
	if isPlainHttpRefused {
		return fmt.Errorf("oidc login: issuer_url %q must use https, http is only accepted on the loopback", l.IssuerUrl)
	}
	return nil
}

type OidcAccessToken struct {
	// Audience access tokens must be issued for, required
	Audience string `yaml:"audience"`
	// Claim of access tokens holding the username (default: `sub`)
	UsernameClaim string `yaml:"username_claim"`
}

func (l *OidcAccessToken) UnmarshalYAML(unmarshal func(any) error) error {
	type plain OidcAccessToken
	err := unmarshal((*plain)(l))
	if err != nil {
		return err
	}
	if l.Audience == "" {
		return fmt.Errorf("oidc login: access_token.audience is required")
	}
	if l.UsernameClaim == "" {
		l.UsernameClaim = "sub"
	}
	return nil
}
