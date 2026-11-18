package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"github.com/seagalia/flock/controller/config"
)

type BasicAuth struct {
	opts []*config.BasicAuthLogin
}

func NewBasicAuth(opts []*config.BasicAuthLogin) *BasicAuth {
	return &BasicAuth{
		opts: opts,
	}
}

func (b *BasicAuth) Type() string {
	return string(AuthTypeBasic)
}

func (b *BasicAuth) LoginUser(username, password string) LoginFunc {
	return func(_ context.Context) (Identity, bool, error) {
		var userOpt *config.BasicAuthLogin
		usernameHash := sha256.Sum256([]byte(username))
		passwordHash := sha256.Sum256([]byte(password))
		for _, opt := range b.opts {
			expectedUsernameHash := sha256.Sum256([]byte(opt.Username))
			expectedPasswordHash := sha256.Sum256([]byte(opt.Password))
			usernameMatch := subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:]) == 1
			passwordMatch := subtle.ConstantTimeCompare(passwordHash[:], expectedPasswordHash[:]) == 1
			if usernameMatch && passwordMatch {
				userOpt = opt
				break
			}
		}
		if userOpt == nil {
			return Identity{}, false, nil
		}
		return Identity{
			Username: username,
			Groups:   userOpt.Groups,
		}, true, nil
	}

}
