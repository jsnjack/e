package ai

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	probeInterval   = 30 * time.Second
	failureCooldown = time.Minute
)

type providerState struct {
	provider Provider
	mu       sync.Mutex
	healthy  bool
	checked  bool
	failedAt time.Time
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
		state := state
		state.mu.Lock()
		state.checked = true
		state.healthy = false
		state.mu.Unlock()
		go func() {
			probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()
			err := state.provider.Healthy(probeCtx)
			state.mu.Lock()
			state.checked = true
			state.healthy = err == nil
			if err == nil {
				state.failedAt = time.Time{}
			}
			state.mu.Unlock()
		}()
	}
}

func (s *providerState) available() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	s.checked = true
	s.healthy = err == nil
	if err == nil {
		s.failedAt = time.Time{}
	} else {
		s.failedAt = time.Now()
	}
}

// Complete asks the first available provider, falling through on pre-output errors.
func (c *Chain) Complete(ctx context.Context, req Request) (string, error) {
	result, err := c.complete(ctx, req)
	return result.Text, err
}

func (c *Chain) complete(ctx context.Context, req Request) (Result, error) {
	if len(c.providers) == 0 {
		return Result{}, fmt.Errorf("no autocomplete models configured")
	}
	var errs []error
	tried := 0
	for index, state := range c.providers {
		if !state.available() {
			continue
		}
		tried++
		result, err := state.provider.Complete(ctx, req)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
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
	if tried == 0 {
		return Result{}, fmt.Errorf("no autocomplete model is currently available")
	}
	return Result{}, errors.Join(errs...)
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
