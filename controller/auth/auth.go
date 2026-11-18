package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/seagalia/flock/controller/config"
)

const (
	AuthTypeLdap  AuthType = "LDAP"
	AuthTypeBasic AuthType = "BASIC"
	AuthTypeOidc  AuthType = "OIDC"
)

type AuthType string

// Identity is the user a login or a token stands for
type Identity struct {
	Username string
	Groups   []string
}

// ErrProviderUnavailable is wrapped by the errors of a TokenVerifier which could not reach its provider
var ErrProviderUnavailable = errors.New("oidc provider unavailable")

// AuthLogin is the login service of the controller
type AuthLogin interface {
	Type() string
}

// LoginFunc gives the identity of a successful login, false when credentials are wrong
type LoginFunc func(ctx context.Context) (Identity, bool, error)

// AuthUserPassword is a login where the controller checks a username and a password, then makes the token
type AuthUserPassword interface {
	LoginUser(username, password string) LoginFunc
}

// TokenVerifier is a login where users get their token from a provider, the controller only verifies it
type TokenVerifier interface {
	// Issuer is the iss the verifier accepts, the server routes tokens on it.
	Issuer() string
	// VerifyToken checks an ID token for the client id, then a JWT access token for access_token.audience when
	// configured. A provider that can't be discovered yet gives an error wrapping ErrProviderUnavailable.
	VerifyToken(ctx context.Context, token string) (Identity, error)
}

// NewLogin builds the login service of the config, logins checking passwords are protected by the lockout
func NewLogin(conf *config.ServerLogin) (AuthLogin, error) {
	lockoutDuration := time.Duration(conf.LockoutDuration)
	switch conf.Type {
	case "basic":
		return NewProtect(NewBasicAuth(conf.BasicAuth), conf.LockoutAfterFailures, lockoutDuration), nil
	case "ldap":
		return NewProtect(NewLdapAuth(conf.Ldap), conf.LockoutAfterFailures, lockoutDuration), nil
	case "oidc":
		return NewOidcLogin(conf.Oidc), nil
	}
	return nil, fmt.Errorf("unknown login type %s", conf.Type)
}
