package ai

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	label string
	text  string
	err   error
}

type probingProvider struct {
	started chan struct{}
	release chan struct{}
}

func (p probingProvider) Complete(context.Context, Request) (string, error) { return "ready", nil }
func (p probingProvider) Healthy(context.Context) error {
	close(p.started)
	<-p.release
	return nil
}
func (p probingProvider) Label() string { return "probing" }

func (p fakeProvider) Complete(context.Context, Request) (string, error) { return p.text, p.err }
func (p fakeProvider) Healthy(context.Context) error                     { return p.err }
func (p fakeProvider) Label() string                                     { return p.label }

func TestChainFallback(t *testing.T) {
	tests := []struct {
		name      string
		providers []Provider
		want      string
		fallback  bool
	}{
		{"primary", []Provider{fakeProvider{label: "one", text: "a"}}, "a", false},
		{"fallback", []Provider{fakeProvider{label: "one", err: errors.New("down")}, fakeProvider{label: "two", text: "b"}}, "b", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			completer := NewCompleter(tc.providers)
			got, err := completer.CompleteDetailed(context.Background(), Request{Prefix: "x"})
			if err != nil {
				t.Fatal(err)
			}
			if got.Text != tc.want {
				t.Fatalf("got %q, want %q", got.Text, tc.want)
			}
			_, fallback := completer.ActiveModel()
			if fallback != tc.fallback {
				t.Fatalf("fallback = %v, want %v", fallback, tc.fallback)
			}
		})
	}
}

func TestAvailabilityProbeDoesNotTemporarilyDisableProvider(t *testing.T) {
	provider := probingProvider{started: make(chan struct{}), release: make(chan struct{})}
	chain := NewChain([]Provider{provider})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chain.Start(ctx)
	<-provider.started
	result, err := chain.complete(ctx, Request{Prefix: "x"})
	close(provider.release)
	if err != nil || result.Text != "ready" {
		t.Fatalf("completion during probe = %q, %v", result.Text, err)
	}
}

func TestGenerationTimeoutBenchesProviderBriefly(t *testing.T) {
	state := &providerState{checked: true, healthy: true}
	state.result(fmt.Errorf("request: %w", context.DeadlineExceeded))
	if state.available(true) {
		t.Fatal("a timed-out provider stayed first in line")
	}
	if !state.available(false) {
		t.Fatal("a slow generation marked the provider unreachable")
	}
	state.mu.Lock()
	state.slowUntil = time.Now().Add(-time.Second)
	state.mu.Unlock()
	if !state.available(true) {
		t.Fatal("provider stayed benched after slowCooldown")
	}
}

type countingProvider struct {
	label string
	text  string
	err   error
	calls atomic.Int32
}

func (p *countingProvider) Complete(context.Context, Request) (string, error) {
	p.calls.Add(1)
	return p.text, p.err
}
func (p *countingProvider) Healthy(context.Context) error { return nil }
func (p *countingProvider) Label() string                 { return p.label }

func TestTimedOutProviderIsSkipped(t *testing.T) {
	timeout := fmt.Errorf("request: %w", context.DeadlineExceeded)
	tests := []struct {
		name         string
		others       []*countingProvider
		wantSlowHits int32
		wantText     string
		wantErr      bool
	}{
		{name: "fallback answers while primary is benched", others: []*countingProvider{{label: "fast", text: "ok"}}, wantSlowHits: 1, wantText: "ok"},
		{name: "only provider is still tried", wantSlowHits: 2, wantErr: true},
		{name: "benched provider is tried when others are down", others: []*countingProvider{{label: "down", err: errors.New("offline")}}, wantSlowHits: 2, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			slow := &countingProvider{label: "slow", err: timeout}
			providers := []Provider{slow}
			for _, other := range test.others {
				providers = append(providers, other)
			}
			chain := NewChain(providers)
			if _, err := chain.complete(context.Background(), Request{Prefix: "a"}); err == nil && len(test.others) == 0 {
				t.Fatal("timed-out request succeeded")
			}
			// A provider that failed outright is put on cooldown; the
			// "down" case relies on that to leave only the benched one.
			result, err := chain.complete(context.Background(), Request{Prefix: "b"})
			if (err != nil) != test.wantErr || result.Text != test.wantText {
				t.Fatalf("second request = %q, %v", result.Text, err)
			}
			if got := slow.calls.Load(); got != test.wantSlowHits {
				t.Fatalf("slow provider called %d times, want %d", got, test.wantSlowHits)
			}
		})
	}
}

func TestCallerTimeoutDoesNotBenchProvider(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := &countingProvider{label: "p", err: context.DeadlineExceeded}
	chain := NewChain([]Provider{provider, fakeProvider{label: "two", text: "b"}})
	if _, err := chain.complete(ctx, Request{Prefix: "x"}); err == nil {
		t.Fatal("cancelled request succeeded")
	}
	if !chain.providers[0].available(true) {
		t.Fatal("caller cancellation benched the provider")
	}
}

func TestRepeatedProbeFailureKeepsFirstFailureTime(t *testing.T) {
	state := &providerState{provider: fakeProvider{label: "down", err: errors.New("offline")}}
	state.probe(context.Background())
	first := state.failedAt
	if first.IsZero() {
		t.Fatal("probe failure was not recorded")
	}
	state.mu.Lock()
	state.failedAt = first.Add(-failureCooldown)
	state.mu.Unlock()
	state.probe(context.Background())
	if !state.available(true) {
		t.Fatal("repeated probe failure restarted failureCooldown")
	}
}

func TestCompleteDetailedReportsServingModelAndCache(t *testing.T) {
	completer := NewCompleter([]Provider{
		fakeProvider{label: "primary", err: errors.New("offline")},
		fakeProvider{label: "local-gemma", text: "result"},
	})
	req := Request{Prefix: "prefix"}
	result, err := completer.CompleteDetailed(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "result" || result.Model != "local-gemma" || !result.Fallback || result.Cached {
		t.Fatalf("fresh result = %#v", result)
	}
	result, err = completer.CompleteDetailed(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Cached || result.Model != "local-gemma" || !result.Fallback {
		t.Fatalf("cached result = %#v", result)
	}
}

func TestClean(t *testing.T) {
	tests := []struct {
		name   string
		result string
		req    Request
		want   string
	}{
		{"fence", "```go\nworld```", Request{Language: "go"}, "world"},
		// A one-line overlap is ambiguous ("next" may be the model's own
		// word), so it is kept; only multi-line overlaps are removed.
		{"single-line suffix overlap kept", "hello next", Request{Suffix: "next line"}, "hello next"},
		{"two-byte echo kept", "abc", Request{Prefix: "s := ab"}, "abc"},
		{"line echo removed", "func main() {", Request{Prefix: "package main\n\nfunc ma"}, "in() {"},
		{"closing brace kept", "if ok {\n\treturn\n}", Request{Suffix: "\n}\n"}, "if ok {\n\treturn\n}"},
		{"short multi-line overlap kept", "x()\n\treturn\n}", Request{Suffix: "\treturn\n}\n"}, "x()\n\treturn\n}"},
		{"full repeated line removed", "x := 1\nreturn value\n", Request{Suffix: "return value\n}\n"}, "x := 1\n"},
		{"repeated lines removed", "a()\n\tdefer cancel()\n}", Request{Suffix: "\n\tdefer cancel()\n}\nnext()"}, "a()"},
		{"overlap ending mid-line kept", "a()\nfoo := bar", Request{Suffix: "\nfoo := barbaz"}, "a()\nfoo := bar"},
		{"fim tokens", "<|fim_middle|>value<|endoftext|>", Request{}, "value"},
		{"line echo", "func main() {", Request{Prefix: "package main\n\nfunc ma"}, "in() {"},
		{"line echo of whole line", "\tx := 1", Request{Prefix: "{\n\tx :"}, "= 1"},
		{"one-byte echo kept", "ab", Request{Prefix: "x\nca"}, "ab"},
		{"no echo", "Println()", Request{Prefix: "fmt."}, "Println()"},
		{"duplicate indent", "\treturn", Request{Prefix: "{\n\t"}, "return"},
		{"duplicate space", " world", Request{Prefix: "hello "}, "world"},
		{"newline not stripped as indent", "\nnext", Request{Prefix: "line\n"}, "\nnext"},
		{"trailing blank lines", "value\n\n\n", Request{}, "value\n"},
		{"trailing whitespace lines", "value\n  \n\t\n", Request{}, "value\n"},
		{"two trailing newlines", "value\n\n", Request{}, "value\n"},
		{"single trailing newline kept", "value\n", Request{}, "value\n"},
		{"trailing spaces on content kept", "value  ", Request{}, "value  "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Clean(tc.result, tc.req); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
