package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// projectEntry is one child of a project directory as the sidebar lists it.
type projectEntry struct {
	path string
	dir  bool
}

// projectSplice replaces removals items at position with additions. A list of
// splices is applied in order, so each position refers to the list after the
// previous splices ran.
type projectSplice struct {
	position  int
	removals  int
	additions []projectEntry
}

// readProjectDir lists directory for the sidebar. Version-control metadata is
// skipped; folders come first, then names in case-insensitive order.
func readProjectDir(directory string) ([]projectEntry, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read project directory: %w", err)
	}
	result := make([]projectEntry, 0, len(entries))
	for _, entry := range entries {
		switch entry.Name() {
		case ".git", ".hg", ".svn":
			continue
		}
		result = append(result, projectEntry{path: filepath.Join(directory, entry.Name()), dir: entry.IsDir()})
	}
	sortProjectEntries(result)
	return result, nil
}

func sortProjectEntries(entries []projectEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].dir != entries[j].dir {
			return entries[i].dir
		}
		return strings.ToLower(filepath.Base(entries[i].path)) < strings.ToLower(filepath.Base(entries[j].path))
	})
}

// diffProjectEntries returns the splices that turn old into next. Entries
// present in both lists stay in place, so the tree keeps their rows and the
// expansion state of folders below them.
func diffProjectEntries(old, next []projectEntry) []projectSplice {
	inOld := make(map[projectEntry]bool, len(old))
	for _, entry := range old {
		inOld[entry] = true
	}
	inNext := make(map[projectEntry]bool, len(next))
	for _, entry := range next {
		inNext[entry] = true
	}
	var splices []projectSplice
	var current *projectSplice
	flush := func() {
		if current != nil {
			splices = append(splices, *current)
			current = nil
		}
	}
	start := func(position int) {
		if current == nil {
			current = &projectSplice{position: position}
		}
	}
	position, i, j := 0, 0, 0
	for i < len(old) || j < len(next) {
		switch {
		case i < len(old) && j < len(next) && old[i] == next[j]:
			flush()
			i++
			j++
			position++
		case i < len(old) && !inNext[old[i]]:
			start(position)
			current.removals++
			i++
		case j < len(next) && !inOld[next[j]]:
			start(position)
			current.additions = append(current.additions, next[j])
			j++
			position++
		case i < len(old):
			// The entry moved. Remove it here; it is added again where it
			// now belongs.
			delete(inOld, old[i])
			start(position)
			current.removals++
			i++
		default:
			start(position)
			current.additions = append(current.additions, next[j])
			j++
			position++
		}
	}
	flush()
	return splices
}
