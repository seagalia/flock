// Package leases takes short leases on resources from the controller, so that only one agent of an instance works on
// a resource at a time. A held lease renews itself until Release.
package leases

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	svclease "github.com/seagalia/flock/sdk/flock/services/lease/v1"
	"github.com/seagalia/flock/sdk/flock/services/lease/v1/svcleaseconnect"
	"github.com/seagalia/flock/sdk/logging"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	// DefaultTTL is the life of a lease when Acquire gets no WithTTL, the controller's own default.
	DefaultTTL = time.Minute
	// MinTTL and MaxTTL are the bounds the controller brings a ttl within
	MinTTL             = 10 * time.Second
	MaxTTL             = 5 * time.Minute
	defaultWaitPollMax = 5 * time.Second
	minWait            = 10 * time.Millisecond
)

// ErrLost is the cause of the end of Lease.Context once the controller no longer knows the lease as ours.
var ErrLost = errors.New("lease lost")

// waitPollMax and minTTL are defaultWaitPollMax and MinTTL, vars only so specs don't wait the real bounds
var (
	waitPollMax = defaultWaitPollMax
	minTTL      = MinTTL
)

// HeldError answers Acquire without WithWait when another agent holds the lease.
type HeldError struct {
	HolderUuid string
	HolderName string
	ExpiresAt  time.Time
}

func (e *HeldError) Error() string {
	expiry := e.ExpiresAt.Format(time.RFC3339)
	return fmt.Sprintf("lease held by agent %s (%s) until %s", e.HolderName, e.HolderUuid, expiry)
}

type acquireOptions struct {
	wait bool
	ttl  time.Duration
}

// Option changes how Acquire takes a lease.
type Option func(*acquireOptions)

// WithWait makes Acquire wait until it holds the lease, like a mutex, instead of answering a HeldError.
func WithWait() Option {
	return func(o *acquireOptions) { o.wait = true }
}

// WithTTL sets the life of the lease without a renewal, DefaultTTL when unset. Acquire brings it within the bounds of
// the controller, 10s and 5m.
func WithTTL(ttl time.Duration) Option {
	return func(o *acquireOptions) { o.ttl = ttl }
}

// Client takes leases for the agents of one instance.
type Client struct {
	client       svcleaseconnect.LeaseClient
	instanceUuid string
}

func NewClient(client svcleaseconnect.LeaseClient, instanceUuid string) *Client {
	return &Client{client: client, instanceUuid: instanceUuid}
}

// Acquire takes the lease of a resource. Without WithWait a lease held by another agent answers a *HeldError, with it
// Acquire retries until it holds the lease or ctx ends. An error of the controller always returns at once. The lease
// renews itself until Release, which the caller must call.
func (c *Client) Acquire(ctx context.Context, typeUrl, uuid string, opts ...Option) (*Lease, error) {
	options := acquireOptions{ttl: DefaultTTL}
	for _, opt := range opts {
		opt(&options)
	}
	// the controller brings the ttl within its bounds anyway, asking the same keeps the renewal period and the call
	// bounds in line with the granted ttl
	ttl := min(max(options.ttl, minTTL), MaxTTL)
	req := &svclease.AcquireRequest{
		TypeUrl:      typeUrl,
		UUID:         uuid,
		InstanceUuid: c.instanceUuid,
		Ttl:          durationpb.New(ttl),
	}
	for {
		resp, err := c.acquireOnce(ctx, req, ttl)
		if err != nil {
			return nil, fmt.Errorf("acquire lease of %s %s: %w", typeUrl, uuid, err)
		}
		if resp.GetGranted() {
			return c.startLease(ctx, resp, ttl), nil
		}
		held := &HeldError{
			HolderUuid: resp.GetHolderUuid(),
			HolderName: resp.GetHolderName(),
			ExpiresAt:  resp.GetExpiresAt().AsTime(),
		}
		if !options.wait {
			return nil, held
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(waitBeforeRetry(held.ExpiresAt)):
		}
	}
}

// acquireOnce is bounded by ttl because a grant answered later than that is already expired.
func (c *Client) acquireOnce(
	ctx context.Context, req *svclease.AcquireRequest, ttl time.Duration,
) (*svclease.AcquireResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, ttl)
	defer cancel()
	return c.client.Acquire(callCtx, req)
}

// waitBeforeRetry waits for the expiry of the held lease, but at most waitPollMax so that an early Release of the
// holder is seen soon.
func waitBeforeRetry(expiresAt time.Time) time.Duration {
	wait := min(time.Until(expiresAt), waitPollMax)
	return max(wait, minWait)
}

func (c *Client) startLease(ctx context.Context, resp *svclease.AcquireResponse, ttl time.Duration) *Lease {
	leaseCtx, cancelLease := context.WithCancelCause(ctx)
	// the renewal outlives the ctx of Acquire and only Release or a loss stops it, so a holder whose work ended still
	// gives the lease back
	renewalCtx, stopRenewal := context.WithCancel(context.WithoutCancel(ctx))
	l := &Lease{
		id:          resp.GetLeaseId(),
		ttl:         ttl,
		client:      c.client,
		ctx:         leaseCtx,
		cancelCtx:   cancelLease,
		stopRenewal: stopRenewal,
		renewalDone: make(chan struct{}),
		expiresAt:   resp.GetExpiresAt().AsTime(),
	}
	go l.renewUntilStopped(renewalCtx)
	return l
}

// Lease is a lease held on a resource. It renews itself every ttl/3 until Release or until it is lost.
type Lease struct {
	id          string
	ttl         time.Duration
	client      svcleaseconnect.LeaseClient
	ctx         context.Context
	cancelCtx   context.CancelCauseFunc
	stopRenewal context.CancelFunc
	renewalDone chan struct{}

	mu        sync.Mutex
	expiresAt time.Time
	lost      bool
}

func (l *Lease) renewalPeriod() time.Duration {
	return l.ttl / 3
}

func (l *Lease) Id() string {
	return l.id
}

// Context ends when the ctx given to Acquire ends or when the lease ends, lost (cause ErrLost) or released. Work done
// under the lease runs with it.
func (l *Lease) Context() context.Context {
	return l.ctx
}

// Renew pushes the end of the lease now, ErrLost when the controller no longer knows it as ours. Call it before a
// write that must happen under the lease.
func (l *Lease) Renew(ctx context.Context) error {
	return l.renewOnce(ctx, l.ttl)
}

// Release stops the renewal, waits for it, then gives the lease back. It is safe after a loss and twice.
func (l *Lease) Release(ctx context.Context) error {
	l.stopRenewal()
	<-l.renewalDone
	l.cancelCtx(nil)
	if l.isLost() {
		return nil
	}
	// past the ttl the lease is expired and a release changes nothing
	callCtx, cancel := context.WithTimeout(ctx, l.ttl)
	defer cancel()
	_, err := l.client.Release(callCtx, &svclease.ReleaseRequest{LeaseId: l.id})
	if err != nil {
		return fmt.Errorf("release lease %s: %w", l.id, err)
	}
	return nil
}

func (l *Lease) renewUntilStopped(ctx context.Context) {
	defer close(l.renewalDone)
	logger := logging.Component("leases")
	period := l.renewalPeriod()
	var lastErr error
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(min(period, time.Until(l.knownExpiry()))):
		}
		// the controller may only be unreachable for a while, so the lease stays ours until its last known expiry,
		// and not longer since another agent may take it from then on
		untilExpiry := time.Until(l.knownExpiry())
		if untilExpiry <= 0 {
			logger.Warn().Err(lastErr).Str("lease_id", l.id).Msg("lease lost, not renewed before its expiry")
			l.markLost()
			return
		}
		lastErr = l.renewOnce(ctx, min(period, untilExpiry))
		if lastErr == nil {
			continue
		}
		stopped := ctx.Err() != nil
		if errors.Is(lastErr, ErrLost) || stopped {
			return
		}
		logger.Warn().Err(lastErr).Str("lease_id", l.id).Msg("renew lease, retrying")
	}
}

func (l *Lease) renewOnce(ctx context.Context, timeout time.Duration) error {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	resp, err := l.client.Renew(callCtx, &svclease.RenewRequest{LeaseId: l.id, Ttl: durationpb.New(l.ttl)})
	if connect.CodeOf(err) == connect.CodeNotFound {
		l.markLost()
		return ErrLost
	}
	if err != nil {
		return fmt.Errorf("renew lease %s: %w", l.id, err)
	}
	l.mu.Lock()
	l.expiresAt = resp.GetExpiresAt().AsTime()
	l.mu.Unlock()
	return nil
}

func (l *Lease) markLost() {
	l.mu.Lock()
	l.lost = true
	l.mu.Unlock()
	l.cancelCtx(ErrLost)
}

func (l *Lease) isLost() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lost
}

func (l *Lease) knownExpiry() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.expiresAt
}
