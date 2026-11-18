package svclogin

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	// BreakGlassUsername is reserved: no login service can give it to a user
	BreakGlassUsername   = "break-glass"
	BreakGlassAmr        = "break-glass"
	BreakGlassReasonKey  = "flock_reason"
	BreakGlassDefaultTTL = 15 * time.Minute
	BreakGlassMaxTTL     = time.Hour
)

// BreakGlassClaims is what a break-glass token says about its issuance, for the audit log
type BreakGlassClaims struct {
	ID        string
	Reason    string
	ExpiresAt time.Time
}

// MakeBreakGlassToken makes a short-lived admin token that needs no login provider to be checked, the reason and the
// token id are kept in its claims so that every use of it can be traced back to its issuance
func (j *JwtManager) MakeBreakGlassToken(reason string, ttl time.Duration) (string, time.Time, error) {
	if reason == "" {
		return "", time.Time{}, errors.New("a break-glass token needs a reason")
	}
	if ttl <= 0 || ttl > BreakGlassMaxTTL {
		return "", time.Time{}, fmt.Errorf("break-glass ttl must be above 0 and at most %s", BreakGlassMaxTTL)
	}
	extra := jwt.MapClaims{
		"amr":               []string{BreakGlassAmr},
		BreakGlassReasonKey: reason,
		"jti":               uuid.NewString(),
	}
	return j.makeToken(BreakGlassUsername, []string{}, ttl, extra)
}

// BreakGlassClaims reads the break-glass claims of a token VerifyToken already accepted, it gives nil for a regular
// token. The lifetime is checked again because the key signs regular tokens of any lifetime.
func (j *JwtManager) BreakGlassClaims(token string) (*BreakGlassClaims, error) {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return nil, err
	}
	amr, _ := claims["amr"].([]any) //nolint:errcheck
	isBreakGlass := slices.Contains(amr, any(BreakGlassAmr))
	if !isBreakGlass {
		return nil, nil
	}
	id, _ := claims["jti"].(string) //nolint:errcheck
	if id == "" {
		return nil, errors.New("break-glass token has no jti")
	}
	expiresAt, err := claims.GetExpirationTime()
	if err != nil || expiresAt == nil {
		return nil, errors.New("break-glass token has no exp")
	}
	issuedAt, err := claims.GetIssuedAt()
	if err != nil || issuedAt == nil {
		return nil, errors.New("break-glass token has no iat")
	}
	if expiresAt.Sub(issuedAt.Time) > BreakGlassMaxTTL {
		return nil, fmt.Errorf("break-glass token lives longer than %s", BreakGlassMaxTTL)
	}
	reason, _ := claims[BreakGlassReasonKey].(string) //nolint:errcheck
	return &BreakGlassClaims{ID: id, Reason: reason, ExpiresAt: expiresAt.Time}, nil
}
