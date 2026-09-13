// Package workspace provides bounded, cancellable project discovery.
package workspace

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
)

var ignoredDirectories = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
}

// Files returns relative file paths beneath root. Discovery is bounded so a
// generated tree cannot exhaust memory or monopolize disk access.
func Files(ctx context.Context, root string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100_000
	}
	files := make([]string, 0, min(limit, 4096))
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() {
			if path != root && ignoredDirectories[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= limit {
			return fs.SkipAll
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr == nil {
			files = append(files, relative)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}
