package nm

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/avast/retry-go/v5"
	"github.com/hashicorp/go-multierror"
)

const (
	wildcardSyncAttempts  = 5
	wildcardSyncFirstWait = time.Second
	// the same bound as a transaction of the handlers, a hung nft would hold r.mu for every handler
	wildcardSyncTimeout = 30 * time.Second
)

// MachineIPs gives the addresses of the machine a wildcard vip stands for, by family (syscall.AF_INET or
// syscall.AF_INET6)
type MachineIPs interface {
	Current(family int) []net.IP
}

func (k vsKey) isWildcard() bool {
	ip, _, err := net.ParseCIDR(k.cidr)
	return err == nil && ip.IsUnspecified()
}

// updateWildcard changes the rules of a wildcard vip, then sets them on each address of the machine. r.mu must be
// held.
func (r *nftRules) updateWildcard(ctx context.Context, key vsKey, change func(rules *vsRules)) error {
	next := r.wildcards[key]
	change(&next)
	// a wildcard with no rule left stays until expand removed its copies, so a sync retries a removal that failed
	r.wildcards[key] = next
	return r.expand(ctx, key)
}

// updateConcrete changes the rules of a vip with a real address. A vip whose rules land on the copy of a wildcard
// takes the address from the wildcard, which takes it back once those rules are all gone. r.mu must be held.
func (r *nftRules) updateConcrete(ctx context.Context, key vsKey, change func(rules *vsRules)) error {
	owner, isCopy := r.copies[key]
	current := r.vss[key]
	if isCopy {
		current = vsRules{}
	}
	next := current
	change(&next)
	// handlers clear the l3 log of every vs on each update, a concrete vip with no rule leaves the copy alone
	if isCopy && next == (vsRules{}) {
		return nil
	}

	if err := r.apply(ctx, key, next); err != nil {
		return err
	}
	if isCopy {
		delete(r.copies, key)
		r.entry.Warn().Str("cidr", key.cidr).Uint16("port", key.port).Str("wildcard", owner.cidr).
			Msg("a concrete vip sets rules on this address, the wildcard vip leaves it to it")
	}
	if next == (vsRules{}) {
		return r.expandOn(ctx, key)
	}
	return nil
}

// syncWildcards follows a change of the addresses of the machine on every wildcard vip. A failure is retried a few
// times with a doubled wait, since nothing else asks again for a rule that failed.
func (r *nftRules) syncWildcards(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	return retry.New(
		retry.Context(ctx),
		retry.Attempts(wildcardSyncAttempts),
		retry.Delay(wildcardSyncFirstWait),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.OnRetry(func(attempt uint, err error) {
			// the caller logs the last failure
			isLast := attempt+1 == wildcardSyncAttempts
			if isLast {
				return
			}
			r.entry.Warn().Err(err).Uint("attempt", attempt+1).Msg("failed to follow the addresses on the wildcard vips")
		}),
	).Do(func() error {
		attemptCtx, cancel := context.WithTimeout(ctx, wildcardSyncTimeout)
		defer cancel()
		return r.syncWildcardsOnce(attemptCtx)
	})
}

func (r *nftRules) syncWildcardsOnce(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result error
	for key := range r.wildcards {
		if err := r.expand(ctx, key); err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
}

// r.mu must be held
func (r *nftRules) expandOn(ctx context.Context, key vsKey) error {
	var result error
	for wildcardKey := range r.wildcards {
		if wildcardKey.port != key.port || wildcardKey.isUdp != key.isUdp {
			continue
		}
		if err := r.expand(ctx, wildcardKey); err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
}

// wantedCopies gives the keys of the addresses of the machine the wildcard of wildcardKey stands for, the ones a
// concrete vip holds left out. r.mu must be held.
func (r *nftRules) wantedCopies(wildcardKey vsKey) map[vsKey]bool {
	wanted := make(map[vsKey]bool)
	rules := r.wildcards[wildcardKey]
	if rules == (vsRules{}) || r.addresses == nil {
		return wanted
	}
	family, bits := syscall.AF_INET, 32
	if _, addrType := wildcardKey.addressFamily(); addrType == "ipv6_addr" {
		family, bits = syscall.AF_INET6, 128
	}
	for _, ip := range r.addresses.Current(family) {
		key := newVsKey(&net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}, wildcardKey.port, wildcardKey.isUdp)
		owner, isCopy := r.copies[key]
		isOtherWildcard := isCopy && owner != wildcardKey
		isConcrete := !isCopy && r.vss[key] != (vsRules{})
		if isConcrete {
			r.entry.Debug().Str("cidr", key.cidr).Uint16("port", key.port).Str("wildcard", wildcardKey.cidr).
				Msg("a concrete vip has rules on this address, the wildcard vip leaves it to it")
			continue
		}
		if isOtherWildcard {
			continue
		}
		wanted[key] = true
	}
	return wanted
}

// expand sets the rules of a wildcard on the addresses of the machine, and removes them from the addresses gone.
// r.mu must be held.
func (r *nftRules) expand(ctx context.Context, wildcardKey vsKey) error {
	rules := r.wildcards[wildcardKey]
	wanted := r.wantedCopies(wildcardKey)
	var result error
	for key, owner := range r.copies {
		if owner != wildcardKey || wanted[key] {
			continue
		}
		if err := r.apply(ctx, key, vsRules{}); err != nil {
			result = multierror.Append(result, fmt.Errorf("remove the rules of %s: %w", key.cidr, err))
			continue
		}
		delete(r.copies, key)
	}
	for key := range wanted {
		if err := r.apply(ctx, key, rules); err != nil {
			result = multierror.Append(result, fmt.Errorf("set the rules on %s: %w", key.cidr, err))
			continue
		}
		r.copies[key] = wildcardKey
	}
	isRemoved := rules == (vsRules{}) && !r.ownsCopies(wildcardKey)
	if isRemoved {
		delete(r.wildcards, wildcardKey)
	}
	return result
}

// r.mu must be held
func (r *nftRules) ownsCopies(wildcardKey vsKey) bool {
	for _, owner := range r.copies {
		if owner == wildcardKey {
			return true
		}
	}
	return false
}
