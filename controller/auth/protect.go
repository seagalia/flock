package auth

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"sync"
	"time"
)

// passwordLogin is a login checking passwords, the logins Protect can guard
type passwordLogin interface {
	AuthLogin
	AuthUserPassword
}

type Protect struct {
	authLogin            passwordLogin
	lockoutAfterFailures int
	lockoutFor           time.Duration
	bufUserTry           *sync.Map
	bufUserLockout       *sync.Map
}

func NewProtect(authLogin passwordLogin, lockoutAfterFailures int, lockoutFor time.Duration) *Protect {
	return &Protect{
		authLogin:            authLogin,
		lockoutAfterFailures: lockoutAfterFailures,
		lockoutFor:           lockoutFor,
		bufUserTry:           &sync.Map{},
		bufUserLockout:       &sync.Map{},
	}
}

func (p *Protect) Type() string {
	return p.authLogin.Type()
}

func (p *Protect) LoginUser(username, password string) LoginFunc {
	return func(ctx context.Context) (Identity, bool, error) {
		isLocked, remainingTime := p.userIsLocked(username)
		if isLocked {
			return Identity{}, false, fmt.Errorf("User %s is locked-out for %s", username, remainingTime)
		}
		stats.loginLockedOut.WithLabelValues(username).Set(0)
		identity, ok, err := p.authLogin.LoginUser(username, password)(ctx)
		if err != nil {
			p.bufUserTry.Delete(username)
			return Identity{}, false, err
		}
		if ok {
			stats.loginSuccess.WithLabelValues(username).Inc()
			return identity, ok, nil
		} else {
			stats.loginFailure.WithLabelValues(username).Inc()
		}
		if p.getUserTry(username) >= p.lockoutAfterFailures {
			stats.loginLockedOut.WithLabelValues(username).Set(1)
			p.bufUserLockout.Store(username, time.Now().Add(p.lockoutFor))
			p.bufUserTry.Delete(username)
			p.notify(username)
			return Identity{}, false, fmt.Errorf("User %s is locked-out for %s", username, p.lockoutFor)
		}
		return identity, ok, nil
	}
}

func (p *Protect) notify(username string) {
	log.Warn().
		Str("component", "auth_protect").
		Str("login_type", p.authLogin.Type()).
		Str("username", username).
		Msgf("User is locked-out from authentication for %s", p.lockoutFor)
}

func (p *Protect) getUserTry(username string) int {
	userTry, ok := p.bufUserTry.Load(username)
	if !ok {
		userTry = 0
	}
	p.bufUserTry.Store(username, userTry.(int)+1)
	return userTry.(int)
}

func (p *Protect) userIsLocked(username string) (bool, time.Duration) {
	whenDelock, ok := p.bufUserLockout.Load(username)
	if !ok {
		return false, 0
	}
	if whenDelock.(time.Time).After(time.Now()) {
		return true, time.Until(whenDelock.(time.Time))
	}
	p.bufUserLockout.Delete(username)
	return false, 0
}
