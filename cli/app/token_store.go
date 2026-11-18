package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"github.com/seagalia/flock/sdk/flock/services/login/v1/svcloginconnect"
	"golang.org/x/oauth2"
	"google.golang.org/protobuf/types/known/emptypb"
)

const defaultLoginCallTimeout = 30 * time.Second

// storedToken is the session file at sessionPath(host), written 0600 in a 0700 directory.
type storedToken struct {
	Kind         string `json:"kind"` // "flock", "oidc_user", "oidc_client"
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IdToken      string `json:"id_token,omitempty"`
	// Issuer is the provider of an oidc session, its refresh token is only ever sent back to that provider
	Issuer string    `json:"issuer,omitempty"`
	Expiry time.Time `json:"expiry"`
}

// readStoredToken gives nil when there is no session file
func readStoredToken(path string) (*storedToken, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tok := &storedToken{}
	err = json.Unmarshal(raw, tok)
	if err != nil {
		return nil, fmt.Errorf("invalid session file %s, run flock login again: %w", path, err)
	}
	return tok, nil
}

func writeStoredToken(path string, tok *storedToken) error {
	raw, err := json.Marshal(tok)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	err = os.MkdirAll(dir, 0700)
	if err != nil {
		return err
	}
	// MkdirAll keeps the permissions of a directory that already exists, a looser one would expose the refresh token
	err = os.Chmod(dir, 0700)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, ".session-*", raw)
}

// bearer gives the token sent to the controller, the ID token for a user of the provider: the controller verifies
// ID tokens for the client id of the cli, the access token of a user is meant for the provider only.
func (t *storedToken) bearer() *oauth2.Token {
	if t.Kind == "oidc_user" {
		return &oauth2.Token{AccessToken: t.IdToken, Expiry: t.Expiry}
	}
	return &oauth2.Token{AccessToken: t.AccessToken, Expiry: t.Expiry}
}

var (
	errSessionExpired  = errors.New("your session expired, run flock login again")
	errProviderChanged = errors.New("the controller changed its OIDC provider, run flock login again")
)

// newTokenSource gives the source of the bearer for cnf: FLOCK_TOKEN first, else the stored session (refreshed
// through the provider for oidc_user, renewed with client credentials for oidc_client, an error asking to log in
// again for an expired flock token). For oidc_user the returned Token carries the ID token in AccessToken.
func newTokenSource(ctx context.Context, cnf *Config, path string) (oauth2.TokenSource, error) {
	if cnf.Token != "" {
		return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: cnf.Token}), nil
	}
	client, err := NewClient(cnf)
	if err != nil {
		return nil, err
	}
	return oauth2.ReuseTokenSource(nil, &sessionTokenSource{
		ctx:         withOidcHttpClient(ctx),
		path:        path,
		cnf:         cnf,
		loginClient: svcloginconnect.NewLoginClient(client),
	}), nil
}

// sessionTokenSource gives the bearer of the session file and renews it once expired. The cli keeps no secret: a
// flock token is renewed only when the credentials are given again, by flock login or by FLOCK_USERNAME and
// FLOCK_PASSWORD, a machine token only with FLOCK_CLIENT_ID and FLOCK_CLIENT_SECRET.
type sessionTokenSource struct {
	ctx         context.Context
	path        string
	cnf         *Config
	loginClient svcloginconnect.LoginClient
}

// loginCallTimeout bounds a renewal of the session, a variable so specs can shorten it. The connect client has no
// timeout in gRPC mode, so a controller accepting the connection without answering would block every command forever.
var loginCallTimeout = defaultLoginCallTimeout

func (s *sessionTokenSource) Token() (*oauth2.Token, error) {
	stored, err := readStoredToken(s.path)
	if err != nil {
		return nil, err
	}
	if stored != nil && stored.bearer().Valid() {
		return stored.bearer(), nil
	}

	ctx, cancel := context.WithTimeout(s.ctx, loginCallTimeout)
	defer cancel()
	var renewed *storedToken
	switch {
	case stored == nil || stored.Kind == "flock":
		renewed, err = s.loginFlock(ctx, stored != nil)
	case stored.Kind == "oidc_user":
		renewed, err = s.refreshOidcUser(ctx, stored)
	case stored.Kind == "oidc_client":
		renewed, err = s.renewOidcClient(ctx)
	default:
		return nil, fmt.Errorf("unknown session kind %q in %s, run flock login again", stored.Kind, s.path)
	}
	if err != nil {
		return nil, err
	}
	err = writeStoredToken(s.path, renewed)
	if err != nil {
		return nil, fmt.Errorf("error saving session file %s: %w", s.path, err)
	}
	return renewed.bearer(), nil
}

func (s *sessionTokenSource) loginFlock(ctx context.Context, hadSession bool) (*storedToken, error) {
	hasCredentials := s.cnf.Username != "" && s.cnf.Password != ""
	if !hasCredentials && !hadSession {
		return nil, errors.New("you are not logged in, run flock login")
	}
	if !hasCredentials {
		return nil, errSessionExpired
	}
	resp, err := s.loginClient.GetToken(ctx, &svclogin.GetTokenRequest{
		Username: s.cnf.Username,
		Password: s.cnf.Password,
	})
	if err != nil {
		return nil, err
	}
	return &storedToken{Kind: "flock", AccessToken: resp.GetToken(), Expiry: resp.GetExpiresAt().AsTime()}, nil
}

func (s *sessionTokenSource) refreshOidcUser(ctx context.Context, stored *storedToken) (*storedToken, error) {
	if stored.RefreshToken == "" {
		return nil, errSessionExpired
	}
	provider, err := s.oidcProvider(ctx)
	if err != nil {
		return nil, err
	}
	if provider.GetIssuerUrl() != stored.Issuer {
		return nil, errProviderChanged
	}
	discovered, conf, err := discoverOidc(ctx, provider)
	if err != nil {
		return nil, err
	}
	// without access token oauth2 refreshes at once, the access token can outlive the ID token we need
	tok, err := conf.TokenSource(ctx, &oauth2.Token{RefreshToken: stored.RefreshToken}).Token()
	if err != nil {
		return nil, fmt.Errorf("error refreshing the session, run flock login again: %w", err)
	}
	rawIdToken, _ := tok.Extra("id_token").(string)
	if rawIdToken == "" {
		return nil, errors.New("the provider did not renew the ID token, run flock login again")
	}
	return userSession(ctx, discovered, provider.GetClientId(), tok, "")
}

func (s *sessionTokenSource) renewOidcClient(ctx context.Context) (*storedToken, error) {
	hasCredentials := s.cnf.ClientId != "" && s.cnf.ClientSecret != ""
	if !hasCredentials {
		return nil, errSessionExpired
	}
	provider, err := s.oidcProvider(ctx)
	if err != nil {
		return nil, err
	}
	return loginOidcClientCredentials(ctx, provider, s.cnf.ClientId, s.cnf.ClientSecret)
}

// oidcProvider asks the controller for its provider, so a session follows a change of the provider configuration
func (s *sessionTokenSource) oidcProvider(ctx context.Context) (*svclogin.OidcProvider, error) {
	serviceAuth, err := s.loginClient.GetServiceAuth(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, err
	}
	if serviceAuth.GetOidc() == nil {
		return nil, errors.New("the controller no longer logs in with OIDC, run flock login again")
	}
	return serviceAuth.GetOidc(), nil
}

// storedOnlyTokenSource gives the bearer of the session file as it is and never asks the controller for a new one.
// Shell completion uses it because a renewal can take longer than the user waits on a tab key.
type storedOnlyTokenSource struct {
	path string
}

func (s storedOnlyTokenSource) Token() (*oauth2.Token, error) {
	stored, err := readStoredToken(s.path)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, errors.New("you are not logged in, run flock login")
	}
	if !stored.bearer().Valid() {
		return nil, errSessionExpired
	}
	return stored.bearer(), nil
}
