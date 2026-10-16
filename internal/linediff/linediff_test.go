package linediff

import (
	"reflect"
	"strings"
	"testing"
)

func lines(text string) []uint64 { return Lines(text) }

func TestCompare(t *testing.T) {
	tests := []struct {
		name, old, next string
		want            []Hunk
	}{
		{name: "same", old: "a\nb\n", next: "a\nb\n"},
		{name: "crlf", old: "a\r\nb\r\n", next: "a\nb\n"},
		{name: "added", old: "a\nc", next: "a\nb\nc", want: []Hunk{{Added, 1, 1}}},
		{name: "added at end", old: "a", next: "a\nb\nc", want: []Hunk{{Added, 1, 2}}},
		{name: "modified", old: "a\nb\nc", next: "a\nB\nc", want: []Hunk{{Modified, 1, 1}}},
		{name: "deleted", old: "a\nb\nc", next: "a\nc", want: []Hunk{{Kind: Deleted, Line: 1}}},
		{name: "deleted first", old: "a\nb", next: "b", want: []Hunk{{Kind: Deleted, Line: 0}}},
		{
			name: "several", old: "1\n2\n3\n4\n5\n6", next: "1\nx\n3\n4\n6\ny",
			want: []Hunk{{Modified, 1, 1}, {Kind: Deleted, Line: 4}, {Added, 5, 1}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Compare(lines(test.old), lines(test.next)); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Compare() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestCompareFallsBackPastLimit(t *testing.T) {
	var old, next strings.Builder
	for i := range maxEdits {
		old.WriteString("o" + string(rune('a'+i%26)) + strings.Repeat("x", i) + "\n")
		next.WriteString("n" + strings.Repeat("y", i) + "\n")
	}
	got := Compare(lines("top\n"+old.String()+"end"), lines("top\n"+next.String()+"end"))
	want := []Hunk{{Modified, 1, maxEdits}}
	// Both sides hold maxEdits changed lines, so nothing shows as added.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Compare() = %+v, want %+v", got, want)
	}
}

func BenchmarkCompareLargeSmallEdit(b *testing.B) {
	var text strings.Builder
	for i := range 100_000 {
		text.WriteString("line " + strings.Repeat("z", i%40) + "\n")
	}
	old := lines(text.String())
	next := append([]uint64(nil), old...)
	next[50_000] = 1
	b.ResetTimer()
	for b.Loop() {
		Compare(old, next)
	}
}

func TestCompareInsertThenModify(t *testing.T) {
	old := "package main\n\nfunc a() {}\n\nfunc b() {}\n"
	next := "package main\n\nfunc a() {}\nfunc added() {}\n\nfunc B() {}\n"
	want := []Hunk{{Added, 3, 1}, {Modified, 5, 1}}
	if got := Compare(lines(old), lines(next)); !reflect.DeepEqual(got, want) {
		t.Fatalf("Compare() = %+v, want %+v", got, want)
	}
	next += "// tail\n"
	want = []Hunk{{Added, 3, 1}, {Modified, 5, 1}, {Added, 6, 1}}
	if got := Compare(lines(old), lines(next)); !reflect.DeepEqual(got, want) {
		t.Fatalf("Compare() = %+v, want %+v", got, want)
	}
}
