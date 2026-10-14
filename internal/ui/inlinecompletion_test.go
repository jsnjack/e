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

func TestGhostDisplay(t *testing.T) {
	tests := []struct {
		name, suggestion, shown, hint string
	}{
		{"single line", " ghost", " ghost", ""},
		{"two lines", " ghost\nnext", " ghost", "  +1 line"},
		{"crlf lines", "a\r\nb\r\nc", "a", "  +2 lines"},
		{"leading break", "\nnext", "↵ next", ""},
		{"leading break and more", "\nnext\nmore", "↵ next", "  +1 line"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			shown, hint := ghostDisplay(test.suggestion)
			if shown != test.shown || hint != test.hint {
				t.Fatalf("ghostDisplay(%q) = %q, %q, want %q, %q", test.suggestion, shown, hint, test.shown, test.hint)
			}
		})
	}
}

func TestExpandTabs(t *testing.T) {
	tests := []struct {
		text          string
		column, width int
		want          string
	}{
		{"\tx", 0, 4, "    x"},
		{"\tx", 1, 4, "   x"},
		{"a\tb", 2, 4, "a b"},
		{"\t", 0, 0, "        "},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			if got := expandTabs(test.text, test.column, test.width); got != test.want {
				t.Fatalf("expandTabs(%q, %d, %d) = %q, want %q", test.text, test.column, test.width, got, test.want)
			}
		})
	}
}

func TestVisualWidth(t *testing.T) {
	tests := []struct {
		text  string
		width int
		want  int
	}{
		{"a\tbc", 4, 6},
		{"\t", 0, 8},
		{"abc", 4, 3},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			if got := visualWidth(test.text, test.width); got != test.want {
				t.Fatalf("visualWidth(%q, %d) = %d, want %d", test.text, test.width, got, test.want)
			}
		})
	}
}
