package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/seagalia/flock/controller/config"
	"net"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// defaultLdapTimeout bounds the dial and each request to the directory, one that accepts the connection and never
// answers would otherwise hold the login forever
const defaultLdapTimeout = 10 * time.Second

// ldapTimeout is a variable so the specs can shorten it
var ldapTimeout = defaultLdapTimeout

type LdapAuth struct {
	options *config.LdapLogin
}

func NewLdapAuth(options *config.LdapLogin) *LdapAuth {
	return &LdapAuth{
		options: options,
	}
}

func (l *LdapAuth) Type() string {
	return string(AuthTypeLdap)
}

func (l *LdapAuth) LoginUser(user, password string) LoginFunc {
	return func(ctx context.Context) (Identity, bool, error) {
		conn, err := l.createConn(ctx)
		if err != nil {
			return Identity{}, false, fmt.Errorf("invalid ldap for '%s': %s", l.options.Address, err.Error())
		}
		defer conn.Close()
		// the ldap library has no context, closing the connection is what makes a pending request return
		stopClosing := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stopClosing()
		searchRequest := ldap.NewSearchRequest(
			l.options.SearchBaseDns,
			ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
			// escaped, a username like "adm*" would otherwise match the entry of another user
			fmt.Sprintf("(&"+l.options.SearchFilter+")", ldap.EscapeFilter(user)),
			[]string{"dn"},
			nil,
		)

		sr, err := conn.Search(searchRequest)
		if err != nil {

			return Identity{}, false, fmt.Errorf("invalid ldap search for '%s': %s", l.options.Address, err.Error())
		}

		if len(sr.Entries) != 1 {
			return Identity{}, false, nil
		}

		userdn := sr.Entries[0].DN

		// Bind as the user to verify their password
		err = conn.Bind(userdn, password)
		// a wrong password is a failed login, not an error: only failed logins count towards the lockout
		isWrongPassword := ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials)
		if isWrongPassword {
			return Identity{}, false, nil
		}
		if err != nil {
			return Identity{}, false, fmt.Errorf("invalid ldap for '%s': %s", l.options.Address, err.Error())
		}
		// users usually cannot read the groups of the directory, bind_dn can
		err = conn.Bind(l.options.BindDn, l.options.BindPassword)
		if err != nil {
			return Identity{}, false, fmt.Errorf("invalid ldap bind for '%s': %s", l.options.Address, err.Error())
		}
		groups, err := l.findLdapGroup(userdn, conn)
		if err != nil {
			return Identity{}, false, fmt.Errorf("invalid ldap group search for '%s': %s", l.options.Address, err.Error())
		}
		return Identity{
			Username: user,
			Groups:   groups,
		}, true, nil
	}
}

func ldapURL(address string, useSsl bool) string {
	if useSsl {
		return "ldaps://" + address
	}
	return "ldap://" + address
}

func (l *LdapAuth) createConn(ctx context.Context) (*ldap.Conn, error) {
	err := ctx.Err()
	if err != nil {
		return nil, err
	}
	tlsConf := &tls.Config{InsecureSkipVerify: l.options.InsecureSkipVerify}
	conn, err := ldap.DialURL(
		ldapURL(l.options.Address, l.options.UseSsl),
		ldap.DialWithTLSConfig(tlsConf),
		ldap.DialWithDialer(&net.Dialer{Timeout: ldapTimeout}),
	)
	if err != nil {
		return nil, err
	}
	conn.SetTimeout(ldapTimeout)
	// the first bind runs before LoginUser can watch ctx
	stopClosing := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClosing()
	err = conn.Bind(l.options.BindDn, l.options.BindPassword)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func (l *LdapAuth) findLdapGroup(dn string, conn *ldap.Conn) ([]string, error) {
	groups := make([]string, 0)
	if l.options.GroupSearchBaseDns == "" || l.options.GroupSearchFilter == "" {
		return groups, nil
	}
	searchRequest := ldap.NewSearchRequest(
		l.options.GroupSearchBaseDns,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf(l.options.GroupSearchFilter, ldap.EscapeFilter(dn)),
		[]string{l.options.GroupAttribute},
		nil,
	)
	sr, err := conn.Search(searchRequest)
	if err != nil {
		return groups, err
	}

	for _, entry := range sr.Entries {
		groups = append(groups, entry.GetAttributeValue(l.options.GroupAttribute))
	}
	return groups, nil
}
