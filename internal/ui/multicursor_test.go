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
	text := "id ID valid id_x é id"
	if got := findExactMatches(text, "id", true); !reflect.DeepEqual(got, []textRange{{0, 2}, {19, 21}}) {
		t.Fatalf("whole-word matches = %v", got)
	}
	if got := findExactMatches(text, "id", false); len(got) != 4 {
		t.Fatalf("substring matches = %v, want 4", got)
	}
}

func TestBestCompletion(t *testing.T) {
	items := []lsp.Completion{{Label: "Errorf"}, {Label: "Println"}, {Label: "Printf", FilterText: "printf"}}
	if item, ok := bestCompletion(items, "pri"); !ok || item.Label != "Println" {
		t.Fatalf("bestCompletion(pri) = %v, %v", item.Label, ok)
	}
	if _, ok := bestCompletion(items, "zzz"); ok {
		t.Fatal("bestCompletion matched an unrelated word")
	}
	if item, ok := bestCompletion(items, ""); !ok || item.Label != "Errorf" {
		t.Fatalf("bestCompletion(\"\") = %v", item.Label)
	}
}
