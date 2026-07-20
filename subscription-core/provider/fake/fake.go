// Package fake is an in-memory BillingProvider for tests.
package fake

import (
	"errors"
	"sync"

	"subscriptioncore/domain"
	"subscriptioncore/provider"
)

// ErrBadSignature mirrors a signature verification failure. It aliases the
// shared port sentinel so callers can match on provider.ErrInvalidSignature.
var ErrBadSignature = provider.ErrInvalidSignature

// Provider implements provider.BillingProvider in memory.
type Provider struct {
	mu     sync.Mutex
	subs   map[string]domain.Subscription
	events map[string]domain.Event // keyed by signature token
}

// New returns an empty fake provider.
func New() *Provider {
	return &Provider{
		subs:   map[string]domain.Subscription{},
		events: map[string]domain.Event{},
	}
}

// StageEvent registers the event that VerifyAndParse returns for `signature`.
func (p *Provider) StageEvent(signature string, e domain.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events[signature] = e
}

// SetSubscription sets what FetchSubscription returns for a provider sub id.
func (p *Provider) SetSubscription(s domain.Subscription) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subs[s.ProviderSubID] = s
}

func (p *Provider) VerifyAndParse(_ []byte, signature string) (domain.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.events[signature]
	if !ok {
		return domain.Event{}, ErrBadSignature
	}
	return e, nil
}

func (p *Provider) FetchSubscription(id string) (domain.Subscription, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.subs[id]
	if !ok {
		return domain.Subscription{}, errors.New("subscription not found")
	}
	return s, nil
}
