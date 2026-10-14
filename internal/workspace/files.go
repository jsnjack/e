// Package workspace provides bounded, cancellable project discovery.
package workspace

import (
	"bytes"
	"context"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
)

// ignoredDirectories are skipped when the project is not a Git repository.
// Dependency and cache folders would otherwise fill the file limit before
// the sources are reached.
var ignoredDirectories = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "__pycache__": true, ".venv": true, ".tox": true,
	".mypy_cache": true, ".pytest_cache": true,
}

// Files returns relative file paths beneath root. Discovery is bounded so a
// generated tree cannot exhaust memory or monopolize disk access.
func Files(ctx context.Context, root string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100_000
	}
	if files, ok := gitFiles(ctx, root, limit); ok {
		return files, nil
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

// gitFiles lists tracked and untracked files that .gitignore does not
// exclude. It reports false when root is not inside a Git work tree or git is
// not installed.
func gitFiles(ctx context.Context, root string, limit int) ([]string, bool) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-z",
		"--cached", "--others", "--exclude-standard", "--deduplicate")
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	files := make([]string, 0, min(limit, 4096))
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		if len(files) >= limit {
			break
		}
		files = append(files, filepath.FromSlash(string(name)))
	}
	sort.Strings(files)
	return files, true
}
