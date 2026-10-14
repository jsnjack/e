package ui

import (
	"reflect"
	"testing"

	"github.com/jsnjack/e/internal/lsp"
)

func TestMultiCursorBoundsPreserveDirection(t *testing.T) {
	tests := []struct {
		cursor     multiCursor
		start, end int
	}{
		{multiCursor{anchor: 2, head: 8}, 2, 8},
		{multiCursor{anchor: 8, head: 2}, 2, 8},
		{multiCursor{anchor: 4, head: 4}, 4, 4},
	}
	for _, test := range tests {
		start, end := test.cursor.bounds()
		if start != test.start || end != test.end {
			t.Fatalf("bounds(%+v) = (%d, %d), want (%d, %d)", test.cursor, start, end, test.start, test.end)
		}
	}
}

func TestNormalizeCursorsMergesOverlaps(t *testing.T) {
	tests := []struct {
		name string
		in   []multiCursor
		want []multiCursor
	}{
		{"distinct kept in order", []multiCursor{{5, 5}, {1, 1}}, []multiCursor{{5, 5}, {1, 1}}},
		{"duplicate carets merge", []multiCursor{{0, 0}, {0, 0}, {0, 0}}, []multiCursor{{0, 0}}},
		{"overlap merges", []multiCursor{{0, 5}, {3, 8}}, []multiCursor{{0, 8}}},
		{"touching ranges stay apart", []multiCursor{{0, 3}, {3, 6}}, []multiCursor{{0, 3}, {3, 6}}},
		{"primary stays last", []multiCursor{{9, 9}, {0, 4}, {2, 2}}, []multiCursor{{9, 9}, {0, 4}}},
		{"primary inside merge stays last", []multiCursor{{9, 9}, {2, 2}, {0, 4}}, []multiCursor{{9, 9}, {0, 4}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := &editorDocument{multiCursors: append([]multiCursor(nil), test.in...)}
			doc.normalizeCursors()
			if !reflect.DeepEqual(doc.multiCursors, test.want) {
				t.Fatalf("normalizeCursors(%v) = %v, want %v", test.in, doc.multiCursors, test.want)
			}
		})
	}
}

func TestFindExactMatches(t *testing.T) {
	const text = "id ID valid id_x é id"
	tests := []struct {
		name      string
		wholeWord bool
		want      []textRange
	}{
		{"whole word skips case and substrings", true, []textRange{{0, 2}, {19, 21}}},
		{"substring keeps case", false, []textRange{{0, 2}, {9, 11}, {12, 14}, {19, 21}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := findExactMatches(text, "id", test.wholeWord); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("findExactMatches = %v, want %v", got, test.want)
			}
		})
	}
}

func TestBestCompletion(t *testing.T) {
	items := []lsp.Completion{{Label: "Errorf"}, {Label: "Println"}, {Label: "Printf", FilterText: "printf"}}
	tests := []struct {
		name, word, want string
		ok               bool
	}{
		{"first match ignoring case", "pri", "Println", true},
		{"no match", "zzz", "", false},
		{"empty word takes the first item", "", "Errorf", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item, ok := bestCompletion(items, test.word)
			if ok != test.ok || item.Label != test.want {
				t.Fatalf("bestCompletion(%q) = %q, %v, want %q, %v", test.word, item.Label, ok, test.want, test.ok)
			}
		})
	}
}
