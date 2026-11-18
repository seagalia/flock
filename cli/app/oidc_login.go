package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	msg "github.com/ArthurHlt/messages"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/int128/oauth2cli"
	"github.com/pkg/browser"
	svclogin "github.com/seagalia/flock/sdk/flock/services/login/v1"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// openBrowser opens the login page of the provider, specs replace it with an http client
var openBrowser = browser.OpenURL

// LoginOidc logs in with the provider of an OIDC controller and stores the session of cnf: client credentials when
// cnf carries a client id, else the device grant when device is set, else the browser on the loopback callback.
func LoginOidc(ctx context.Context, cnf *Config, provider *svclogin.OidcProvider, device bool, callbackPort int) error {
	ctx = withOidcHttpClient(ctx)
	var stored *storedToken
	var err error
	switch {
	case cnf.ClientId != "":
		stored, err = loginOidcClientCredentials(ctx, provider, cnf.ClientId, cnf.ClientSecret)
	case device:
		stored, err = loginOidcDevice(ctx, provider)
	default:
		stored, err = loginOidcLoopback(ctx, provider, callbackPort)
	}
	if err != nil {
		return err
	}
	path, err := sessionPath(cnf.Host)
	if err != nil {
		return err
	}
	return writeStoredToken(path, stored)
}

// oidcHttpTimeout bounds each call to the provider, a variable so specs can shorten it
var oidcHttpTimeout = 30 * time.Second

// withOidcHttpClient gives the http client of the calls to the provider (discovery, keys, token requests) through
// ctx, as go-oidc and oauth2 read it. The timeout bounds each request, never the wait for the user to log in.
func withOidcHttpClient(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, &http.Client{Timeout: oidcHttpTimeout})
}

// discoverOidc gives the provider and the oauth2 config of the cli client, the endpoints come from the discovery
func discoverOidc(ctx context.Context, provider *svclogin.OidcProvider) (*oidc.Provider, *oauth2.Config, error) {
	discovered, err := oidc.NewProvider(ctx, provider.GetIssuerUrl())
	if err != nil {
		return nil, nil, fmt.Errorf("error discovering the OIDC provider %s: %w", provider.GetIssuerUrl(), err)
	}
	conf := &oauth2.Config{
		ClientID: provider.GetClientId(),
		Endpoint: discovered.Endpoint(),
		Scopes:   provider.GetScopes(),
	}
	return discovered, conf, nil
}

// loginOidcLoopback logs the user in with the browser, the provider redirects to a local server on callbackPort, so
// http://127.0.0.1:<callbackPort>/callback must be a redirect uri of the client. oauth2cli generates and checks the
// state.
func loginOidcLoopback(ctx context.Context, provider *svclogin.OidcProvider, callbackPort int) (*storedToken, error) {
	discovered, conf, err := discoverOidc(ctx, provider)
	if err != nil {
		return nil, err
	}
	conf.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/callback", callbackPort)
	verifier := oauth2.GenerateVerifier()
	nonce := rand.Text()

	ready := make(chan string, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case loginUrl := <-ready:
			msg.Infof("Opening %s in your browser to log in", loginUrl)
			err := openBrowser(loginUrl)
			if err != nil {
				msg.Warningf("Could not open a browser (%s), open %s yourself", err, loginUrl)
			}
		case <-done:
		}
	}()
	tok, err := oauth2cli.GetToken(ctx, oauth2cli.Config{
		OAuth2Config:            *conf,
		AuthCodeOptions:         []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)},
		TokenRequestOptions:     []oauth2.AuthCodeOption{oauth2.VerifierOption(verifier)},
		LocalServerBindAddress:  []string{fmt.Sprintf("127.0.0.1:%d", callbackPort)},
		LocalServerCallbackPath: "/callback",
		LocalServerReadyChan:    ready,
	})
	if err != nil {
		return nil, err
	}
	return userSession(ctx, discovered, provider.GetClientId(), tok, nonce)
}

// loginOidcDevice logs the user in with the device grant, for hosts without a browser
func loginOidcDevice(ctx context.Context, provider *svclogin.OidcProvider) (*storedToken, error) {
	discovered, conf, err := discoverOidc(ctx, provider)
	if err != nil {
		return nil, err
	}
	if conf.Endpoint.DeviceAuthURL == "" {
		return nil, fmt.Errorf("the OIDC provider %s does not support the device grant", provider.GetIssuerUrl())
	}
	// RFC 8628 does not ask for PKCE, but providers requiring it on the client (Keycloak) refuse a device login without
	verifier := oauth2.GenerateVerifier()
	auth, err := conf.DeviceAuth(ctx, oauth2.S256ChallengeOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("error starting the device login: %w", err)
	}
	msg.Infof("Open %s and enter the code %s", msg.Cyan(auth.VerificationURI).String(), msg.Cyan(auth.UserCode).String())
	if auth.VerificationURIComplete != "" {
		msg.Infof("Or open %s", msg.Cyan(auth.VerificationURIComplete).String())
	}
	tok, err := conf.DeviceAccessToken(ctx, auth, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("error waiting for the device login: %w", err)
	}
	return userSession(ctx, discovered, provider.GetClientId(), tok, "")
}

// loginOidcClientCredentials logs a machine in, its bearer is the access token: openid is dropped from the scopes
// because no ID token comes with this grant.
func loginOidcClientCredentials(ctx context.Context, provider *svclogin.OidcProvider, clientId, clientSecret string) (*storedToken, error) {
	if clientSecret == "" {
		return nil, errors.New("the client credentials login needs a client secret too")
	}
	_, conf, err := discoverOidc(ctx, provider)
	if err != nil {
		return nil, err
	}
	scopes := slices.DeleteFunc(slices.Clone(provider.GetScopes()), func(scope string) bool { return scope == "openid" })
	machineConf := clientcredentials.Config{
		ClientID:     clientId,
		ClientSecret: clientSecret,
		TokenURL:     conf.Endpoint.TokenURL,
		Scopes:       scopes,
	}
	tok, err := machineConf.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("error logging in with the client credentials: %w", err)
	}
	expiry := tok.Expiry
	if expiry.IsZero() {
		// without expires_in oauth2 gives a token that never expires, it would never be renewed
		expiry = unverifiedExpiry(tok.AccessToken)
	}
	return &storedToken{
		Kind:        "oidc_client",
		AccessToken: tok.AccessToken,
		Issuer:      provider.GetIssuerUrl(),
		Expiry:      expiry,
	}, nil
}

// unverifiedExpiry reads the exp claim of a JWT without checking its signature, the controller verifies the token.
// It gives the zero time when the token is not a JWT or has no exp.
func unverifiedExpiry(rawToken string) time.Time {
	claims := jwt.RegisteredClaims{}
	_, _, err := jwt.NewParser().ParseUnverified(rawToken, &claims)
	if err != nil || claims.ExpiresAt == nil {
		return time.Time{}
	}
	return claims.ExpiresAt.Time
}

// userSession verifies the ID token of tok for the cli client, and its nonce when one was sent
func userSession(ctx context.Context, discovered *oidc.Provider, clientId string, tok *oauth2.Token, nonce string) (*storedToken, error) {
	rawIdToken, _ := tok.Extra("id_token").(string)
	if rawIdToken == "" {
		return nil, errors.New("the OIDC provider gave no ID token, is openid in the scopes?")
	}
	idToken, err := discovered.Verifier(&oidc.Config{ClientID: clientId}).Verify(ctx, rawIdToken)
	if err != nil {
		return nil, fmt.Errorf("invalid ID token: %w", err)
	}
	isNonceWrong := nonce != "" && idToken.Nonce != nonce
	if isNonceWrong {
		return nil, errors.New("invalid ID token: the nonce does not match the login request")
	}
	return &storedToken{
		Kind:         "oidc_user",
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		IdToken:      rawIdToken,
		Issuer:       idToken.Issuer,
		Expiry:       idToken.Expiry,
	}, nil
}
