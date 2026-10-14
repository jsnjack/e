package ai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	probeInterval   = 30 * time.Second
	failureCooldown = time.Minute
	slowCooldown    = 30 * time.Second
)

type providerState struct {
	provider Provider
	mu       sync.Mutex
	healthy  bool
	checked  bool
	failedAt time.Time
	// slowUntil benches a provider whose last generation timed out, so the
	// next requests go to a faster fallback instead of waiting again.
	slowUntil time.Time
}

// Chain tries providers in priority order while skipping known-unavailable entries.
type Chain struct {
	providers []*providerState
	active    atomic.Int32
}

// NewChain constructs an ordered provider chain.
func NewChain(providers []Provider) *Chain {
	states := make([]*providerState, 0, len(providers))
	for _, provider := range providers {
		states = append(states, &providerState{provider: provider})
	}
	return &Chain{providers: states}
}

// Start probes all configured endpoints now and periodically.
func (c *Chain) Start(ctx context.Context) {
	c.probe(ctx)
	go func() {
		ticker := time.NewTicker(probeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.probe(ctx)
			}
		}
	}()
}

func (c *Chain) probe(ctx context.Context) {
	for _, state := range c.providers {
		go state.probe(ctx)
	}
}

func (s *providerState) probe(ctx context.Context) {
	probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	err := s.provider.Healthy(probeCtx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checked = true
	s.healthy = err == nil
	if err == nil {
		s.failedAt = time.Time{}
	} else if s.failedAt.IsZero() {
		// Keep the first failure time; refreshing it on every probe would stop
		// failureCooldown from ever elapsing.
		s.failedAt = time.Now()
	}
}

// available reports whether the provider should be tried. With skipSlow, a
// provider benched after a timeout is skipped until slowUntil passes.
func (s *providerState) available(skipSlow bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if skipSlow && time.Now().Before(s.slowUntil) {
		return false
	}
	return s.reachable()
}

func (s *providerState) reachable() bool {
	if !s.checked {
		return true
	}
	if s.healthy {
		return true
	}
	return !s.failedAt.IsZero() && time.Since(s.failedAt) >= failureCooldown
}

func (s *providerState) result(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil && isTimeout(err) {
		// Slow inference is not evidence that the endpoint is down, so it is
		// not marked failed. It is benched briefly so fallbacks answer first.
		s.checked = false
		s.healthy = false
		s.failedAt = time.Time{}
		s.slowUntil = time.Now().Add(slowCooldown)
		return
	}
	s.slowUntil = time.Time{}
	s.checked = true
	s.healthy = err == nil
	if err == nil {
		s.failedAt = time.Time{}
	} else {
		s.failedAt = time.Now()
	}
}

// complete asks the first available provider, falling through on pre-output
// errors. Providers benched as slow are tried only when no other is available.
func (c *Chain) complete(ctx context.Context, req Request) (Result, error) {
	if len(c.providers) == 0 {
		return Result{}, fmt.Errorf("no autocomplete models configured")
	}
	var errs []error
	tried := make([]bool, len(c.providers))
	attempts := 0
	for _, skipSlow := range []bool{true, false} {
		for index, state := range c.providers {
			if tried[index] || !state.available(skipSlow) {
				continue
			}
			tried[index] = true
			attempts++
			result, err := state.provider.Complete(ctx, req)
			if err != nil && (errors.Is(err, context.Canceled) || ctx.Err() != nil) {
				return Result{}, err
			}
			state.result(err)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", state.provider.Label(), err))
				continue
			}
			c.active.Store(int32(index))
			return Result{Text: result, Model: state.provider.Label(), Fallback: index > 0}, nil
		}
		if attempts > 0 {
			break
		}
	}
	if attempts == 0 {
		return Result{}, fmt.Errorf("no autocomplete model is currently available")
	}
	return Result{}, errors.Join(errs...)
}

// isTimeout reports a provider-side timeout such as http.Client.Timeout.
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// ActiveModel reports which provider last succeeded.
func (c *Chain) ActiveModel() (string, bool) {
	if len(c.providers) == 0 {
		return "", false
	}
	index := int(c.active.Load())
	if index < 0 || index >= len(c.providers) {
		index = 0
	}
	return c.providers[index].provider.Label(), index > 0
}
