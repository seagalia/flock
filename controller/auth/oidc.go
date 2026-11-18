package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/seagalia/flock/controller/config"
)

const (
	// discoveryTimeout bounds the discovery of the provider
	discoveryTimeout = 10 * time.Second

	// defaultProviderHTTPTimeout bounds each request to the provider, the keys endpoint is read lazily by Verify with a
	// client taken from the context given to NewProvider, so this client must carry its own timeout
	defaultProviderHTTPTimeout = 10 * time.Second
)

// providerHTTPTimeout is a variable so the specs can shorten it
var providerHTTPTimeout = defaultProviderHTTPTimeout

// OidcLogin verifies the tokens users and machines get from an OpenID Connect provider, the controller never talks
// to the provider for a login
type OidcLogin struct {
	conf *config.OidcLogin

	// discovery is lazy so the controller starts while the provider is down, verifiers are nil until it succeeds
	mutex          sync.Mutex
	idVerifier     *oidc.IDTokenVerifier
	accessVerifier *oidc.IDTokenVerifier
}

func NewOidcLogin(conf *config.OidcLogin) *OidcLogin {
	return &OidcLogin{
		conf: conf,
	}
}

func (o *OidcLogin) Type() string {
	return string(AuthTypeOidc)
}

func (o *OidcLogin) Issuer() string {
	return o.conf.IssuerUrl
}

// Provider gives what clients need to get a token from the provider
func (o *OidcLogin) Provider() *config.OidcLogin {
	return o.conf
}

func (o *OidcLogin) VerifyToken(ctx context.Context, token string) (Identity, error) {
	idVerifier, accessVerifier, err := o.verifiers(ctx)
	if err != nil {
		return Identity{}, err
	}
	idToken, idErr := idVerifier.Verify(ctx, token)
	if idErr == nil {
		return o.identity(idToken, o.conf.UsernameClaim)
	}
	if accessVerifier == nil {
		return Identity{}, fmt.Errorf("invalid ID token for client %s: %w", o.conf.ClientId, idErr)
	}
	accessToken, accessErr := accessVerifier.Verify(ctx, token)
	if accessErr != nil {
		return Identity{}, fmt.Errorf("invalid ID token for client %s (%w) and invalid access token for audience %s (%w)",
			o.conf.ClientId, idErr, o.conf.AccessToken.Audience, accessErr)
	}
	return o.identity(accessToken, o.conf.AccessToken.UsernameClaim)
}

// verifiers discovers the provider on the first call, a failed discovery is retried by the next call
func (o *OidcLogin) verifiers(ctx context.Context) (*oidc.IDTokenVerifier, *oidc.IDTokenVerifier, error) {
	o.mutex.Lock()
	defer o.mutex.Unlock()
	if o.idVerifier != nil {
		return o.idVerifier, o.accessVerifier, nil
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()
	discoveryCtx = oidc.ClientContext(discoveryCtx, &http.Client{Timeout: providerHTTPTimeout})
	provider, err := oidc.NewProvider(discoveryCtx, o.conf.IssuerUrl)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrProviderUnavailable, err)
	}
	o.idVerifier = provider.Verifier(&oidc.Config{ClientID: o.conf.ClientId})
	if o.conf.AccessToken != nil {
		o.accessVerifier = provider.Verifier(&oidc.Config{ClientID: o.conf.AccessToken.Audience})
	}
	return o.idVerifier, o.accessVerifier, nil
}

func (o *OidcLogin) identity(token *oidc.IDToken, usernameClaim string) (Identity, error) {
	claims := make(map[string]any)
	err := token.Claims(&claims)
	if err != nil {
		return Identity{}, fmt.Errorf("invalid token claims: %w", err)
	}
	username, ok := claims[usernameClaim].(string)
	if !ok || username == "" {
		return Identity{}, fmt.Errorf("claim %s with the username not found in token", usernameClaim)
	}
	return Identity{
		Username: username,
		Groups:   claimGroups(claims[o.conf.GroupsClaim]),
	}, nil
}

// claimGroups reads the groups the way providers give them: a list, a space separated string or the keys of a map
func claimGroups(claim any) []string {
	groups := make([]string, 0)
	switch value := claim.(type) {
	case string:
		groups = append(groups, strings.Fields(value)...)
	case []any:
		for _, group := range value {
			groups = append(groups, fmt.Sprint(group))
		}
	case map[string]any:
		for group := range value {
			groups = append(groups, group)
		}
	}
	return groups
}
