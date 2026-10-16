// Package workspace provides bounded, cancellable project discovery.
package workspace

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

// Entry is one child of a directory listed by ListDir.
type Entry struct {
	Name string
	Dir  bool
}

// ListDir lists up to limit children of dir, folders first, then names in
// case-insensitive order. Symbolic links count as folders when their target
// is one.
func ListDir(dir string, limit int) ([]Entry, error) {
	file, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	children, err := file.ReadDir(limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	entries := make([]Entry, 0, len(children))
	for _, child := range children {
		isDir := child.IsDir()
		if child.Type()&fs.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(dir, child.Name())); err == nil {
				isDir = info.IsDir()
			}
		}
		entries = append(entries, Entry{Name: child.Name(), Dir: isDir})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}
