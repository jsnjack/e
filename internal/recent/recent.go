// Package recent keeps the files and folders E opened, ranked by how often
// and how lately each was opened. It has no GTK dependency.
package recent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jsnjack/e/internal/config"
)

// maxEntries caps the store. Adding past it drops the lowest-ranked entries.
const maxEntries = 300

// Entry is one remembered path.
type Entry struct {
	Path  string `json:"path"`
	Dir   bool   `json:"dir"`
	Count int    `json:"count"`
	Last  int64  `json:"last"`
}

// Load reads the store at path. A missing file is an empty store.
func Load(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read recent paths: %w", err)
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse recent paths: %w", err)
	}
	return entries, nil
}

// Save atomically replaces the store at path.
func Save(path string, entries []Entry) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("encode recent paths: %w", err)
	}
	return config.WriteFileAtomic(path, data, 0o600)
}

// Add records one more open of path at now and returns the updated store.
// The input slice is not modified.
func Add(entries []Entry, path string, dir bool, now time.Time) []Entry {
	result := make([]Entry, 0, len(entries)+1)
	found := false
	for _, entry := range entries {
		if entry.Path == path {
			entry.Count++
			entry.Dir = dir
			entry.Last = now.Unix()
			found = true
		}
		result = append(result, entry)
	}
	if !found {
		result = append(result, Entry{Path: path, Dir: dir, Count: 1, Last: now.Unix()})
	}
	if len(result) > maxEntries {
		result = Ranked(result, now)[:maxEntries]
	}
	return result
}

// Merge adds the entries of older that newer does not hold. It joins a store
// read from disk with paths recorded before the read finished.
func Merge(newer, older []Entry) []Entry {
	seen := make(map[string]bool, len(newer))
	for _, entry := range newer {
		seen[entry.Path] = true
	}
	result := append([]Entry(nil), newer...)
	for _, entry := range older {
		if !seen[entry.Path] {
			result = append(result, entry)
		}
	}
	return result
}

// Ranked returns a copy of entries, best first. An open within the last hour
// counts four times, within a day twice, within a week once, and older opens
// a quarter. This is the zoxide ranking.
func Ranked(entries []Entry, now time.Time) []Entry {
	result := append([]Entry(nil), entries...)
	sort.SliceStable(result, func(i, j int) bool {
		return score(result[i], now) > score(result[j], now)
	})
	return result
}

func score(entry Entry, now time.Time) float64 {
	age := now.Sub(time.Unix(entry.Last, 0))
	weight := 0.25
	switch {
	case age < time.Hour:
		weight = 4
	case age < 24*time.Hour:
		weight = 2
	case age < 7*24*time.Hour:
		weight = 1
	}
	return float64(entry.Count) * weight
}
