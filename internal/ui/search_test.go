package ui

import "testing"

func TestFindMatches(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		query string
		want  []textRange
	}{
		{"plain", "one two one", "one", []textRange{{0, 3}, {8, 11}}},
		{"smart case insensitive", "Alpha alpha", "alpha", []textRange{{0, 5}, {6, 11}}},
		{"smart case sensitive", "Alpha alpha", "Alpha", []textRange{{0, 5}}},
		{"unicode offsets", "é é", "é", []textRange{{0, 1}, {2, 3}}},
		{"empty", "text", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := findMatches(tc.text, tc.query)
			if len(got) != len(tc.want) {
				t.Fatalf("matches = %v, want %v", got, tc.want)
			}
			for index := range got {
				if got[index] != tc.want[index] {
					t.Fatalf("matches = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestEditorHelpers(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"completion word", completionWord("hello world"), "hello"},
		{"completion punctuation", completionWord("name(value)"), "name"},
		{"go language", languageForPath("main.go"), "go"},
		{"markdown language", languageForPath("README.md"), "markdown"},
		{"unknown language", languageForPath("NOTICE"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
