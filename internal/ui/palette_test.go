package ui

import (
	"testing"

	"github.com/diamondburned/gotk4/pkg/gdk/v4"
)

func TestFuzzyScore(t *testing.T) {
	if fuzzyScore("mdp", "Markdown Preview") < 0 {
		t.Fatal("expected ordered fuzzy match")
	}
	if fuzzyScore("xyz", "Markdown Preview") >= 0 {
		t.Fatal("unexpected fuzzy match")
	}
	if fuzzyScore("mark", "Markdown Preview") <= fuzzyScore("mark", "Make Remote Kind") {
		t.Fatal("consecutive match should score higher")
	}
}

func TestPaletteDismissKey(t *testing.T) {
	tests := []struct {
		name   string
		keyval uint
		want   bool
	}{
		{name: "escape", keyval: gdk.KEY_Escape, want: true},
		{name: "enter", keyval: gdk.KEY_Return, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := paletteDismissKey(test.keyval); got != test.want {
				t.Fatalf("paletteDismissKey(%d) = %v, want %v", test.keyval, got, test.want)
			}
		})
	}
}

func TestPaletteItemAt(t *testing.T) {
	items := []paletteItem{{title: "first"}, {title: "second"}}
	tests := []struct {
		name  string
		index int
		want  string
		ok    bool
	}{
		{name: "first", index: 0, want: "first", ok: true},
		{name: "second", index: 1, want: "second", ok: true},
		{name: "negative", index: -1, ok: false},
		{name: "past end", index: 2, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item, ok := paletteItemAt(items, test.index)
			if ok != test.ok || item.title != test.want {
				t.Fatalf("paletteItemAt(%d) = (%q, %v), want (%q, %v)", test.index, item.title, ok, test.want, test.ok)
			}
		})
	}
}

func TestNextPaletteIndex(t *testing.T) {
	tests := []struct {
		name                      string
		current, count, direction int
		want                      int
	}{
		{name: "down", current: 0, count: 3, direction: 1, want: 1},
		{name: "up", current: 2, count: 3, direction: -1, want: 1},
		{name: "clamp start", current: 0, count: 3, direction: -1, want: 0},
		{name: "clamp end", current: 2, count: 3, direction: 1, want: 2},
		{name: "empty", current: 0, count: 0, direction: 1, want: -1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := nextPaletteIndex(test.current, test.count, test.direction); got != test.want {
				t.Fatalf("nextPaletteIndex() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestCommandPaletteRegistry(t *testing.T) {
	items := (&window{}).commandPaletteItems()
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if item.id == "" || item.title == "" || item.run == nil {
			t.Fatalf("incomplete palette command: %+v", item)
		}
		if seen[item.id] {
			t.Fatalf("duplicate palette command id %q", item.id)
		}
		seen[item.id] = true
		matches := filterPaletteItems(items, item.title, maxPaletteResults)
		if len(matches) == 0 || matches[0].id != item.id {
			t.Errorf("exact search for %q selected %#v", item.title, matches)
		}
	}
	if matches := filterPaletteItems(items, "new text file", maxPaletteResults); len(matches) == 0 || matches[0].id != "new" {
		t.Fatalf("new text file search selected %#v", matches)
	}
}
