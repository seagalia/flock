package challenges

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-acme/lego/v5/challenge"

	"github.com/seagalia/flock/sdk/flock/api/config/secret/v1"
	svcsecret "github.com/seagalia/flock/sdk/flock/services/secret/v1"
	"github.com/seagalia/flock/sdk/flock/services/secret/v1/svcsecretconnect"
)

const (
	DefaultEchoTimeout = 30 * time.Second

	updateTimeout = 30 * time.Second
	// maxDataPairs is the limit of the controller's validation on the data of a secret, the marker included
	maxDataPairs = 64
)

var _ challenge.Provider = (*Provider)(nil)

// Provider is the challenge provider of one order. It writes the challenges to the secret of the order and returns
// once the table shows the controller delivered them back, since every replica answers from what ADS delivers.
type Provider struct {
	client      svcsecretconnect.SecretClient
	table       *Table
	base        *secret.Secret
	echoTimeout time.Duration

	// mu is held across the write so two writes of the same secret never reach the controller out of order
	mu   sync.Mutex
	live map[string]Challenge
}

// NewProvider takes the challenge secret as base, its Data is ignored.
func NewProvider(client svcsecretconnect.SecretClient, table *Table, base *secret.Secret, echoTimeout time.Duration) *Provider {
	return &Provider{
		client:      client,
		table:       table,
		base:        base,
		echoTimeout: echoTimeout,
		live:        map[string]Challenge{},
	}
}

func (p *Provider) Present(ctx context.Context, domain, token, keyAuth string) error {
	if err := p.add(ctx, Challenge{Domain: domain, Token: token, KeyAuth: keyAuth}); err != nil {
		return err
	}

	echoCtx, cancel := context.WithTimeout(ctx, p.echoTimeout)
	defer cancel()
	if err := p.table.WaitFor(echoCtx, p.base.GetUUID(), token); err != nil {
		echoErr := fmt.Errorf("challenge of %s not echoed by the controller: %w", domain, err)
		// lego never calls CleanUp after a failed Present, so the challenge is taken out of the secret here
		return errors.Join(echoErr, p.dropAfterFailure(ctx, token))
	}
	return nil
}

// dropAfterFailure must outlive ctx, which may be the very reason the echo failed.
func (p *Provider) dropAfterFailure(ctx context.Context, token string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.live, token)
	return p.write(context.WithoutCancel(ctx))
}

// CleanUp must outlive ctx, which lego cancels once the order times out or stops, otherwise the key authorization
// stays in the secret and blocks the tls-alpn-01 challenges of the same domain for the other certificates.
func (p *Provider) CleanUp(ctx context.Context, _, token, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.live, token)
	return p.write(context.WithoutCancel(ctx))
}

// Flush takes out of the secret every challenge lego left there, once the order is over. It must outlive ctx for the
// same reason as CleanUp.
func (p *Provider) Flush(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.live) == 0 {
		return nil
	}
	clear(p.live)
	return p.write(context.WithoutCancel(ctx))
}

func (p *Provider) add(ctx context.Context, c Challenge) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	_, replacesOne := p.live[c.Token]
	// the marker takes one pair
	isFull := len(p.live)+1 >= maxDataPairs
	if isFull && !replacesOne {
		return fmt.Errorf("challenge of %s refused: a secret holds at most %d challenges", c.Domain, maxDataPairs-1)
	}

	previous, hadPrevious := p.live[c.Token]
	p.live[c.Token] = c
	if err := p.write(ctx); err != nil {
		if hadPrevious {
			p.live[c.Token] = previous
		} else {
			delete(p.live, c.Token)
		}
		return err
	}
	return nil
}

// write sends the live challenges, the caller holds mu.
func (p *Provider) write(ctx context.Context) error {
	live := make([]Challenge, 0, len(p.live))
	for _, c := range p.live {
		live = append(live, c)
	}

	updateCtx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	_, err := p.client.UpdateSecret(updateCtx, &svcsecret.UpdateSecretRequest{
		UUID: p.base.GetUUID(),
		Secret: &secret.Secret{
			Name:     p.base.GetName(),
			Metadata: p.base.GetMetadata(),
			Data:     EncodeData(live),
		},
	})
	if err != nil {
		return fmt.Errorf("write challenges to secret %s: %w", p.base.GetUUID(), err)
	}
	return nil
}
