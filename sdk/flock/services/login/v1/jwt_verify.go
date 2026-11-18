package svclogin

import (
	"fmt"
	"maps"
	"time"

	"github.com/seagalia/flock/sdk/config"
	"github.com/seagalia/flock/sdk/contexes"

	"github.com/golang-jwt/jwt/v5"
)

const (
	skewClock = 3 * time.Minute
)

// JwtManager makes the flock tokens and verifies them with the same key, the public half for RS and ES algorithms
type JwtManager struct {
	jwtMaker  *config.JwtMaker
	signKey   any
	verifyKey any
}

func NewJwtManager(jwtMaker *config.JwtMaker) (*JwtManager, error) {
	signKey, verifyKey, err := jwtMaker.Keys()
	if err != nil {
		return nil, err
	}
	return &JwtManager{
		jwtMaker:  jwtMaker,
		signKey:   signKey,
		verifyKey: verifyKey,
	}, nil
}

// Issuer is the iss of the tokens it makes, the controller routes the tokens it must verify on it
func (j *JwtManager) Issuer() string {
	return j.jwtMaker.Issuer
}

// VerifyToken checks a token made by MakeToken and gives the user it was made for
func (j *JwtManager) VerifyToken(token string) (string, []string, error) {
	parserOpts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{j.jwtMaker.Alg.Alg.Alg()}),
		jwt.WithIssuer(j.jwtMaker.Issuer),
		jwt.WithLeeway(skewClock),
		jwt.WithIssuedAt(),
		jwt.WithExpirationRequired(),
	}
	// golang-jwt requires the claim as soon as the option is given, even empty
	if j.jwtMaker.Audience != "" {
		parserOpts = append(parserOpts, jwt.WithAudience(j.jwtMaker.Audience))
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
		return j.verifyKey, nil
	}, parserOpts...)
	if err != nil {
		return "", nil, err
	}
	username, ok := claims[contexes.UsernameMetaKey].(string)
	if !ok || username == "" {
		return "", nil, fmt.Errorf("username not found in jwt token")
	}
	groups := make([]string, 0)
	rawGroups, _ := claims[contexes.GroupsMetaKey].([]any) //nolint:errcheck
	for _, group := range rawGroups {
		groups = append(groups, fmt.Sprint(group))
	}
	return username, groups, nil
}

func (j *JwtManager) MakeToken(username string, groups []string) (string, time.Time, error) {
	return j.makeToken(username, groups, time.Duration(j.jwtMaker.ExpireIn), nil)
}

// makeToken signs the claims of a flock token valid for ttl, extra claims are added to them
func (j *JwtManager) makeToken(username string, groups []string, ttl time.Duration, extra jwt.MapClaims) (string, time.Time, error) {
	now := time.Now().UTC()
	expireAt := now.Add(ttl)
	claims := make(jwt.MapClaims)
	maps.Copy(claims, extra)
	claims[contexes.UsernameMetaKey] = username
	claims[contexes.GroupsMetaKey] = groups
	claims["exp"] = expireAt.Unix()
	claims["iat"] = now.Unix()
	claims["nbf"] = now.Unix()
	if j.jwtMaker.Audience != "" {
		claims["aud"] = j.jwtMaker.Audience
	}
	claims["iss"] = j.jwtMaker.Issuer

	tok, err := jwt.NewWithClaims(j.jwtMaker.Alg.Alg, claims).SignedString(j.signKey)
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, expireAt, nil
}
