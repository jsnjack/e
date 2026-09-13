package ui

import "testing"

func TestInlineCompletionText(t *testing.T) {
	long := make([]rune, maxInlineCompletionRunes+10)
	for index := range long {
		long[index] = '界'
	}
	tests := []struct {
		name string
		text string
		want string
	}{
		{"single line", " suggestion", " suggestion"},
		{"line feed", " first\nsecond", " first\nsecond"},
		{"windows newline", " first\r\nsecond", " first\r\nsecond"},
		{"leading newline", "\nsecond", "\nsecond"},
		{"unicode limit", string(long), string(long[:maxInlineCompletionRunes])},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := inlineCompletionText(test.text); got != test.want {
				t.Fatalf("inlineCompletionText() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStandaloneESpans(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []int
	}{
		{"words", "E is E.", []int{0, 5}},
		{"identifiers", "EDGE e editor", nil},
		{"unicode boundaries", "αE Eβ “E”", []int{7}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spans := standaloneESpans(test.text)
			got := make([]int, len(spans))
			for index, span := range spans {
				got[index] = span.runeStart
			}
			if len(got) != len(test.want) {
				t.Fatalf("spans = %v, want %v", got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("spans = %v, want %v", got, test.want)
				}
			}
		})
	}
}
