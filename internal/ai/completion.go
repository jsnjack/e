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
	result = trimLineEcho(result, req.Prefix)
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
	result = trimSuffixOverlap(result, req.Suffix)
	return trimTrailingBlankLines(result)
}

// trimLineEcho removes the part of the cursor line that chat models repeat at
// the start of their answer. The longest tail of the line (3 bytes or more)
// that starts result is removed; a 2-byte match is too often the model's own
// continuation. Otherwise a duplicated run of trailing spaces
// and tabs is removed.
func trimLineEcho(result, prefix string) string {
	line := prefix[strings.LastIndexByte(prefix, '\n')+1:]
	for n := min(len(line), len(result)); n >= 3; n-- {
		if strings.HasPrefix(result, line[len(line)-n:]) {
			return result[n:]
		}
	}
	indent := line[len(strings.TrimRight(line, " \t")):]
	if indent != "" && strings.HasPrefix(result, indent) {
		return result[len(indent):]
	}
	return result
}

// trimSuffixOverlap removes text at the end of result that repeats the start
// of the suffix. Only the longest overlap that holds a full line and at least
// 8 non-whitespace bytes is removed. Shorter overlaps such as "\n}" are often
// the model's own closing text and stay.
func trimSuffixOverlap(result, suffix string) string {
	for n := min(len(result), len(suffix)); n > 0; n-- {
		overlap := suffix[:n]
		if !strings.HasSuffix(result, overlap) || !strings.Contains(overlap, "\n") {
			continue
		}
		atLineBoundary := n == len(suffix) || suffix[n] == '\n' || suffix[n-1] == '\n'
		if atLineBoundary && nonSpaceBytes(overlap) >= 8 {
			return result[:len(result)-n]
		}
	}
	return result
}

func nonSpaceBytes(text string) int {
	count := 0
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case ' ', '\t', '\n', '\r', '\v', '\f':
		default:
			count++
		}
	}
	return count
}

// trimTrailingBlankLines drops whitespace-only lines at the end of result and
// keeps one newline after the last line with content.
func trimTrailingBlankLines(result string) string {
	content := len(strings.TrimRight(result, " \t\n"))
	end := strings.IndexByte(result[content:], '\n')
	if end < 0 {
		return result
	}
	return result[:content+end+1]
}
