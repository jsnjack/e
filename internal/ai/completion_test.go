package ai

import (
	"context"
	"errors"
	"testing"
)

type fakeProvider struct {
	label string
	text  string
	err   error
}

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
			got, err := completer.Complete(context.Background(), Request{Prefix: "x"})
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			_, fallback := completer.ActiveModel()
			if fallback != tc.fallback {
				t.Fatalf("fallback = %v, want %v", fallback, tc.fallback)
			}
		})
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
		{"suffix overlap", "hello next", Request{Suffix: "next line"}, "hello "},
		{"fim tokens", "<|fim_middle|>value<|endoftext|>", Request{}, "value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Clean(tc.result, tc.req); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
