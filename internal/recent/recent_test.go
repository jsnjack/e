package recent

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestAddCountsAndRanks(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var entries []Entry
	entries = Add(entries, "/old", false, now.Add(-30*24*time.Hour))
	entries = Add(entries, "/old", false, now.Add(-30*24*time.Hour))
	entries = Add(entries, "/new", true, now)
	if len(entries) != 2 || entries[0].Count != 2 {
		t.Fatalf("Add() = %+v", entries)
	}
	ranked := Ranked(entries, now)
	if ranked[0].Path != "/new" {
		t.Fatalf("Ranked() = %+v, want /new first", ranked)
	}
}

func TestAddTrimsLowestRanked(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	var entries []Entry
	entries = Add(entries, "/stale", false, now.Add(-365*24*time.Hour))
	for i := range maxEntries {
		entries = Add(entries, filepath.Join("/p", string(rune('a'+i%26)), time.Duration(i).String()), false, now)
	}
	if len(entries) != maxEntries {
		t.Fatalf("len = %d, want %d", len(entries), maxEntries)
	}
	for _, entry := range entries {
		if entry.Path == "/stale" {
			t.Fatal("the stale entry survived trimming")
		}
	}
}

func TestMerge(t *testing.T) {
	newer := []Entry{{Path: "/a", Count: 3}}
	older := []Entry{{Path: "/a", Count: 1}, {Path: "/b", Count: 1}}
	want := []Entry{{Path: "/a", Count: 3}, {Path: "/b", Count: 1}}
	if got := Merge(newer, older); !reflect.DeepEqual(got, want) {
		t.Fatalf("Merge() = %+v, want %+v", got, want)
	}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "recent.json")
	if entries, err := Load(path); err != nil || entries != nil {
		t.Fatalf("Load(missing) = %v, %v", entries, err)
	}
	want := []Entry{{Path: "/a", Dir: true, Count: 2, Last: 5}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %+v, %v", got, err)
	}
}
