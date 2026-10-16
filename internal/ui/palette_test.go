package ui

import (
	"path/filepath"
	"strings"
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

func TestFuzzyRanking(t *testing.T) {
	if fuzzyScore("otter", "trace-bright-otter.json.gz") <= fuzzyScore("otter", "Screenshot at 14-58-37 Alert rules.png") {
		t.Fatal("an unbroken match should rank above a scattered one")
	}
	items := withSearchKeys([]paletteItem{{title: "banana.txt", detail: "Downloads"}, {title: "notes.txt", detail: "Documents"}})
	if got := filterPaletteItems(items, "bana dow", 10); len(got) != 1 || got[0].title != "banana.txt" {
		t.Fatalf("spaces in the query should separate words, got %+v", got)
	}
}

// TestFuzzyWordsRankUnbrokenMatches is the case from a screenshot: letters
// of "otter" scattered through other names ranked above the real file.
func TestFuzzyWordsRankUnbrokenMatches(t *testing.T) {
	var items []paletteItem
	for _, relative := range []string{
		"Downloads/tickets_by_version.csv",
		"fsmounts/cc/jsn/cobro/meriyah/test262/download-test262.mjs",
		"fsmounts/cc/jsn/cobro/tests/cobro_selenium/test_downloads.py",
		"Downloads/trace-20260929-bright-otter-39c499b9.json.gz",
		"Downloads/Screenshot 2026-03-30 at 14-58-37 Alert rules - Alerting - Grafana.png",
	} {
		items = append(items, paletteItem{title: relative, key: []rune(strings.ToLower(filepath.Base(relative) + " " + relative))})
	}
	got := filterPaletteItems(items, "dow otter", 10)
	if len(got) != 1 || got[0].title != "Downloads/trace-20260929-bright-otter-39c499b9.json.gz" {
		titles := make([]string, len(got))
		for i, item := range got {
			titles[i] = item.title
		}
		t.Fatalf("dow otter = %q", titles)
	}
}
