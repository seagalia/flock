package challenges

import (
	"context"
	"strings"
	"sync"
)

type entryKey struct {
	secretUuid string
	token      string
}

// waiter is shared by the WaitFor calls of one key, count tells when the last one leaves.
type waiter struct {
	done  chan struct{}
	count int
}

// Table holds the challenge answers of every secret delivered to this replica.
type Table struct {
	mu      sync.Mutex
	entries map[entryKey]Challenge
	// closed and removed by Replace once the entries hold the key, removed by the last WaitFor leaving otherwise
	waiters map[entryKey]*waiter
}

func NewTable() *Table {
	return &Table{
		entries: map[entryKey]Challenge{},
		waiters: map[entryKey]*waiter{},
	}
}

// Replace swaps all the entries of a secret for challenges and wakes the waiters of the tokens they hold.
func (t *Table) Replace(secretUuid string, challenges []Challenge) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.dropLocked(secretUuid)
	for _, c := range challenges {
		key := entryKey{secretUuid: secretUuid, token: c.Token}
		t.entries[key] = c
		if w, found := t.waiters[key]; found {
			close(w.done)
			delete(t.waiters, key)
		}
	}
}

func (t *Table) Drop(secretUuid string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dropLocked(secretUuid)
}

func (t *Table) dropLocked(secretUuid string) {
	for key := range t.entries {
		if key.secretUuid == secretUuid {
			delete(t.entries, key)
		}
	}
}

func (t *Table) KeyAuthByToken(token string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for key, c := range t.entries {
		if key.token == token {
			return c.KeyAuth, true
		}
	}
	return "", false
}

// KeyAuthByDomain answers false when the domain has no entry or when two secrets give it different key
// authorizations, since the answer would be a guess.
func (t *Table) KeyAuthByDomain(domain string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var keyAuth string
	found := false
	for _, c := range t.entries {
		if !strings.EqualFold(c.Domain, domain) {
			continue
		}
		if found && c.KeyAuth != keyAuth {
			return "", false
		}
		keyAuth = c.KeyAuth
		found = true
	}
	return keyAuth, found
}

// WaitFor returns once the entries of the secret hold the token, or the error of ctx.
func (t *Table) WaitFor(ctx context.Context, secretUuid, token string) error {
	key := entryKey{secretUuid: secretUuid, token: token}

	t.mu.Lock()
	if _, found := t.entries[key]; found {
		t.mu.Unlock()
		return nil
	}
	w, found := t.waiters[key]
	if !found {
		w = &waiter{done: make(chan struct{})}
		t.waiters[key] = w
	}
	w.count++
	t.mu.Unlock()

	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		t.leave(key, w)
		return ctx.Err()
	}
}

// leave drops w once its last caller is gone. A Replace may have closed it already and a new WaitFor put another
// waiter under key since, which stays.
func (t *Table) leave(key entryKey, w *waiter) {
	t.mu.Lock()
	defer t.mu.Unlock()

	w.count--
	isCurrent := t.waiters[key] == w
	if isCurrent && w.count == 0 {
		delete(t.waiters, key)
	}
}
