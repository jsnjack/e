// Package ai provides low-latency, availability-aware inline completion.
package ai

import (
	"context"
	"strings"
	"sync"
	"unicode/utf8"
)

// Request is the bounded editor context sent to a model.
type Request struct {
	Prefix   string
	Suffix   string
	Filename string
	Language string
	// Instruction and Selection switch the request from prediction to an
	// explicit, reviewable transformation.
	Instruction string
	Selection   string
}

// Provider generates one insertion at the cursor.
type Provider interface {
	Complete(context.Context, Request) (string, error)
	Healthy(context.Context) error
	Label() string
}

// Result is a generated insertion together with the provider that served it.
type Result struct {
	Text     string
	Model    string
	Fallback bool
	Cached   bool
}

// Completer adds a small exact-context cache around an ordered provider chain.
type Completer struct {
	chain *Chain
	mu    sync.Mutex
	cache map[Request]Result
	order []Request
}

// NewCompleter constructs an autocomplete service.
func NewCompleter(providers []Provider) *Completer {
	return &Completer{chain: NewChain(providers), cache: make(map[Request]Result)}
}

// Start begins availability probes until ctx is cancelled.
func (c *Completer) Start(ctx context.Context) { c.chain.Start(ctx) }

// Complete returns a cached or freshly generated insertion.
func (c *Completer) Complete(ctx context.Context, req Request) (string, error) {
	result, err := c.CompleteDetailed(ctx, req)
	return result.Text, err
}

// CompleteDetailed returns a completion and the exact model that served it.
func (c *Completer) CompleteDetailed(ctx context.Context, req Request) (Result, error) {
	c.mu.Lock()
	if cached, ok := c.cache[req]; ok {
		c.mu.Unlock()
		cached.Cached = true
		return cached, nil
	}
	c.mu.Unlock()
	result, err := c.chain.complete(ctx, req)
	if err != nil {
		return Result{}, err
	}
	result.Text = Clean(result.Text, req)
	if result.Text == "" {
		return result, nil
	}
	c.mu.Lock()
	const maxEntries = 128
	if len(c.order) >= maxEntries {
		delete(c.cache, c.order[0])
		c.order = c.order[1:]
	}
	c.cache[req] = result
	c.order = append(c.order, req)
	c.mu.Unlock()
	return result, nil
}

// ActiveModel returns the most recent provider label and whether it is a fallback.
func (c *Completer) ActiveModel() (string, bool) { return c.chain.ActiveModel() }

// Clean removes response wrappers, echoes, suffix overlap, and unreasonable output.
func Clean(result string, req Request) string {
	result = strings.ReplaceAll(result, "\r\n", "\n")
	result = strings.TrimPrefix(result, "```"+req.Language+"\n")
	result = strings.TrimPrefix(result, "```\n")
	result = strings.TrimSuffix(result, "```")
	result = strings.TrimPrefix(result, req.Prefix)
	for _, token := range []string{"<|fim_middle|>", "<|fim_prefix|>", "<|fim_suffix|>", "<|endoftext|>"} {
		result = strings.ReplaceAll(result, token, "")
	}
	limit := 4096
	if req.Instruction != "" {
		limit = 64 * 1024
	}
	if len(result) > limit {
		result = result[:limit]
		for !utf8.ValidString(result) {
			result = result[:len(result)-1]
		}
	}
	if req.Suffix != "" {
		max := min(len(result), len(req.Suffix))
		for n := max; n > 0; n-- {
			if strings.HasSuffix(result, req.Suffix[:n]) {
				result = strings.TrimSuffix(result, req.Suffix[:n])
				break
			}
		}
	}
	return strings.TrimSuffix(result, "\n\n\n")
}
