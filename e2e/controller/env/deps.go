//go:build e2e

package env

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"slices"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	PostgresImage = "postgres:18-alpine"
	natsImage     = "nats:2.15-alpine"
	oauth2Image   = "ghcr.io/navikt/mock-oauth2-server:6.1.2"
	MysqlImage    = "mysql:8.4.6"
	ldapImage     = "osixia/openldap:1.5.0"

	ldapBaseDn       = "dc=flock,dc=e2e"
	ldapAdminDn      = "cn=admin," + ldapBaseDn
	ldapAdminPwd     = "ldap-admin"
	ldapCustomDir    = "/container/service/slapd/assets/config/bootstrap/ldif/custom/"
	ldapUsersOu      = "ou=users," + ldapBaseDn
	ldapGroupsOu     = "ou=groups," + ldapBaseDn
	LdapUserPassword = "ldap-pwd"

	// Oauth2CodeUser is who every authorization code login on the provider gives, the authorize endpoint has no
	// login form to tell the user
	Oauth2CodeUser = "carol"
)

// Oauth2Users are the users the specs ask ID tokens for, with their groups
var Oauth2Users = map[string][]string{
	"alice": {"team-a"},
	"bob":   {"team-b"},
	"frank": {"team-x"},
	"root":  {"admins"},
}

// LdapGroups are the groups of the ldap server with their members, every user has LdapUserPassword
var LdapGroups = map[string][]string{
	"admins":    {"ldap-root"},
	"team-ldap": {"ldap-alice", "ldap-bob"},
	"team-bob":  {"ldap-bob"},
}

// LdapUsers are the users of the ldap server, ldap-carol is in no group
var LdapUsers = []string{"ldap-alice", "ldap-bob", "ldap-carol", "ldap-root"}

// Oauth2CodeGroups are the groups of Oauth2CodeUser
var Oauth2CodeGroups = []string{"team-c", "team-d"}

// StartPostgres gives a dsn reachable from the containers of the network, each call is a new database server
func (e *Env) StartPostgres(ctx context.Context) (string, error) {
	name := Unique("postgres")
	_, err := e.start(ctx, name, testcontainers.ContainerRequest{
		Image:        PostgresImage,
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "flock",
			"POSTGRES_PASSWORD": "flock",
			"POSTGRES_DB":       "flock",
		},
		// the init script runs a first server that also logs it is ready, then restarts it
		WaitingFor: wait.ForAll(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
			wait.ForListeningPort("5432/tcp"),
		),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("postgres://flock:flock@%s:5432/flock?sslmode=disable", name), nil
}

// StartNats gives a url reachable from the containers of the network
func (e *Env) StartNats(ctx context.Context) (string, error) {
	name := Unique("nats")
	_, err := e.start(ctx, name, testcontainers.ContainerRequest{
		Image:        natsImage,
		ExposedPorts: []string{"4222/tcp"},
		WaitingFor:   wait.ForAll(wait.ForLog("Server is ready"), wait.ForListeningPort("4222/tcp")),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("nats://%s:4222", name), nil
}

// StopDependency stops the container answering url (a dsn of StartPostgres, a url of StartNats) to see the
// controllers lose it
func (e *Env) StopDependency(ctx context.Context, url string) error {
	parsed, err := neturl.Parse(url)
	if err != nil {
		return err
	}
	e.mutex.Lock()
	containers := e.containers
	e.mutex.Unlock()
	for _, c := range containers {
		aliases, err := c.NetworkAliases(ctx)
		isTerminated := err != nil
		if isTerminated {
			continue
		}
		if slices.Contains(aliases[e.Net.Name], parsed.Hostname()) {
			stopTimeout := 10 * time.Second
			return c.Stop(ctx, &stopTimeout)
		}
	}
	return fmt.Errorf("no container answers %s", url)
}

// StartOauth2 starts the OIDC provider of ControllerOptions.Oidc. It has no device authorization endpoint, so the
// device grant of the cli is only covered by the unit specs of cli/app.
func (e *Env) StartOauth2(ctx context.Context) (*Oauth2, error) {
	jsonConfig, err := oauth2JsonConfig()
	if err != nil {
		return nil, err
	}
	name := Unique("oauth2")
	c, err := e.start(ctx, name, testcontainers.ContainerRequest{
		Image:        oauth2Image,
		ExposedPorts: []string{oauth2Port},
		Env:          map[string]string{"JSON_CONFIG": jsonConfig},
		WaitingFor:   wait.ForHTTP("/isalive").WithPort(oauth2Port),
	})
	if err != nil {
		return nil, err
	}
	return &Oauth2{Name: name, Container: c}, nil
}

// oauth2JsonConfig maps the parameters of a token request to the claims of its tokens, the first matching mapping
// wins. The provider sets the aud of an ID token to the client id of the request, the one of an access token comes
// from the claims. Groups are a space separated string because a claim template can only give a string.
func oauth2JsonConfig() (string, error) {
	conf := map[string]any{
		// without it the authorize endpoint shows a login form instead of redirecting
		"interactiveLogin": false,
		"tokenCallbacks": []map[string]any{{
			"issuerId": "default",
			"requestMappings": []map[string]any{
				{
					"requestParam": "grant_type", "match": "client_credentials",
					"claims": map[string]any{"sub": "${clientId}", "aud": []string{OidcAudience}},
				},
				{
					"requestParam": "grant_type", "match": "authorization_code",
					"claims": map[string]any{
						"sub": Oauth2CodeUser, "preferred_username": Oauth2CodeUser, "groups": strings.Join(Oauth2CodeGroups, " "),
					},
				},
				{
					"requestParam": "username", "match": "*",
					"claims": map[string]any{"sub": "${username}", "preferred_username": "${username}", "groups": "${groups}"},
				},
			},
		}},
	}
	b, err := json.Marshal(conf)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// HTTPClient reaches the provider by its name on the network from the host, so the tokens it gives carry the issuer
// the controller expects. Other addresses, like the loopback callback of the cli, are dialed as they are.
func (o *Oauth2) HTTPClient(ctx context.Context) *http.Client {
	providerAddr := o.Name + ":8080"
	hostAddr := o.Addr(ctx)
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if addr == providerAddr {
				addr = hostAddr
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}
}

// IdToken gives an ID token of the flock client for username, its password grant needs no login form
func (o *Oauth2) IdToken(ctx context.Context, username string, groups []string) (string, error) {
	return o.IdTokenForClient(ctx, OidcClientId, username, groups)
}

// IdTokenForClient gives an ID token issued for another client than the one of the controller
func (o *Oauth2) IdTokenForClient(ctx context.Context, clientId, username string, groups []string) (string, error) {
	tok, err := o.token(ctx, neturl.Values{
		"grant_type": {"password"},
		"client_id":  {clientId},
		"scope":      {"openid"},
		"username":   {username},
		"password":   {"any"},
		"groups":     {strings.Join(groups, " ")},
	})
	if err != nil {
		return "", err
	}
	if tok.IdToken == "" {
		return "", fmt.Errorf("provider %s gave no ID token for %s", o.Name, username)
	}
	return tok.IdToken, nil
}

// AccessToken gives the JWT access token of a machine logged in with client credentials, any secret is accepted
func (o *Oauth2) AccessToken(ctx context.Context, clientId string) (string, error) {
	tok, err := o.token(ctx, neturl.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientId},
		"client_secret": {"any"},
		"scope":         {OidcAudience},
	})
	if err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

type oauth2Token struct {
	AccessToken string `json:"access_token"`
	IdToken     string `json:"id_token"`
}

func (o *Oauth2) token(ctx context.Context, form neturl.Values) (*oauth2Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.IssuerURL()+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.HTTPClient(ctx).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider %s answered %d: %s", o.Name, resp.StatusCode, body)
	}
	tok := &oauth2Token{}
	err = json.Unmarshal(body, tok)
	if err != nil {
		return nil, err
	}
	return tok, nil
}

// StartMysql gives a dsn reachable from the containers of the network, each call is a new database server
func (e *Env) StartMysql(ctx context.Context) (string, error) {
	name := Unique("mysql")
	_, err := e.start(ctx, name, testcontainers.ContainerRequest{
		Image:        MysqlImage,
		ExposedPorts: []string{"3306/tcp"},
		Env: map[string]string{
			"MYSQL_ROOT_PASSWORD": "flock-root",
			"MYSQL_USER":          "flock",
			"MYSQL_PASSWORD":      "flock",
			"MYSQL_DATABASE":      "flock",
		},
		// the init script runs a first server without network, the port only opens on the final one
		WaitingFor: wait.ForAll(
			wait.ForLog("port: 3306  MySQL Community Server"),
			wait.ForListeningPort("3306/tcp"),
		).WithDeadline(3 * time.Minute),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("flock:flock@tcp(%s:3306)/flock", name), nil
}

// Ldap is an openldap server seeded with LdapUsers and LdapGroups
type Ldap struct {
	// Name is the container name on the network
	Name string
}

// login is the ldap login of the controller that logs in the users of the server, groups are their groupOfNames
func (l *Ldap) login() map[string]any {
	return map[string]any{
		"type": "ldap",
		"ldap_options": map[string]any{
			"address":               l.Name + ":389",
			"bind_dn":               ldapAdminDn,
			"bind_password":         ldapAdminPwd,
			"search_base_dns":       ldapUsersOu,
			"search_filter":         "(objectClass=inetOrgPerson)(uid=%s)",
			"group_search_base_dns": ldapGroupsOu,
			"group_search_filter":   "(member=%s)",
			"group_attribute":       "cn",
		},
	}
}

// StartLdap must run before the controller using it, the server is seeded when it is returned
func (e *Env) StartLdap(ctx context.Context) (*Ldap, error) {
	name := Unique("ldap")
	ldapSearchSeeded := []string{
		"ldapsearch", "-x", "-H", "ldap://localhost", "-D", ldapAdminDn, "-w", ldapAdminPwd,
		"-b", ldapGroupsOu, "(cn=team-bob)", "dn",
	}
	_, err := e.start(ctx, name, testcontainers.ContainerRequest{
		Image:        ldapImage,
		ExposedPorts: []string{"389/tcp"},
		Env: map[string]string{
			"LDAP_ORGANISATION":   "flock",
			"LDAP_DOMAIN":         "flock.e2e",
			"LDAP_ADMIN_PASSWORD": ldapAdminPwd,
			"LDAP_TLS":            "false",
		},
		// the image only reads the custom ldif from a copy of its service directory, in the order of their names
		Cmd: []string{"--copy-service"},
		Files: []testcontainers.ContainerFile{
			fileFromBytes([]byte(ldapSeedLdif()), ldapCustomDir+"50-flock.ldif", 0o644),
		},
		WaitingFor: wait.ForExec(ldapSearchSeeded).
			WithResponseMatcher(func(body io.Reader) bool {
				out, _ := io.ReadAll(body)
				return strings.Contains(string(out), "numEntries: 1")
			}).
			WithStartupTimeout(3 * time.Minute),
	})
	if err != nil {
		return nil, err
	}
	return &Ldap{Name: name}, nil
}

func ldapSeedLdif() string {
	var ldif strings.Builder
	fmt.Fprintf(&ldif, "dn: %s\nobjectClass: organizationalUnit\nou: users\n\n", ldapUsersOu)
	fmt.Fprintf(&ldif, "dn: %s\nobjectClass: organizationalUnit\nou: groups\n\n", ldapGroupsOu)
	for _, username := range LdapUsers {
		fmt.Fprintf(&ldif, "dn: uid=%s,%s\nobjectClass: inetOrgPerson\nuid: %s\ncn: %s\nsn: %s\nuserPassword: %s\n\n",
			username, ldapUsersOu, username, username, username, LdapUserPassword)
	}
	for group, members := range LdapGroups {
		fmt.Fprintf(&ldif, "dn: cn=%s,%s\nobjectClass: groupOfNames\ncn: %s\n", group, ldapGroupsOu, group)
		for _, member := range members {
			fmt.Fprintf(&ldif, "member: uid=%s,%s\n", member, ldapUsersOu)
		}
		ldif.WriteString("\n")
	}
	return ldif.String()
}
