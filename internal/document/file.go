// Package document owns file metadata and durable disk operations.
package document

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Newline identifies the line ending used when a document was opened.
type Newline string

const (
	// NewlineLF is the Unix line ending.
	NewlineLF Newline = "lf"
	// NewlineCRLF is the Windows line ending.
	NewlineCRLF Newline = "crlf"
	// NewlineCR is the legacy Mac line ending.
	NewlineCR Newline = "cr"
)

// File is an opened document snapshot. Text always uses LF internally.
type File struct {
	Path    string
	Text    []byte
	Newline Newline
	Mode    os.FileMode
	ModTime int64
}

// ErrChangedOnDisk indicates that a checked save would overwrite a newer disk version.
var ErrChangedOnDisk = errors.New("file changed on disk")

// Open reads a UTF-8 text file and normalizes line endings for the editor.
func Open(path string) (File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return File{}, fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return File{}, fmt.Errorf("inspect %s: %w", abs, err)
	}
	if !info.Mode().IsRegular() {
		return File{}, fmt.Errorf("open %s: not a regular file", abs)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return File{}, fmt.Errorf("read %s: %w", abs, err)
	}
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return File{}, fmt.Errorf("open %s: file is not UTF-8 text", abs)
	}
	newline := detectNewline(data)
	data = normalizeNewlines(data)
	return File{Path: abs, Text: data, Newline: newline, Mode: info.Mode().Perm(), ModTime: info.ModTime().UnixNano()}, nil
}

// Save durably replaces path while preserving the requested line ending.
func Save(path string, text []byte, newline Newline, mode os.FileMode) (int64, error) {
	return save(path, text, newline, mode, 0)
}

// SaveChecked saves only when path still has expectedModTime.
func SaveChecked(path string, text []byte, newline Newline, mode os.FileMode, expectedModTime int64) (int64, error) {
	return save(path, text, newline, mode, expectedModTime)
}

func save(path string, text []byte, newline Newline, mode os.FileMode, expectedModTime int64) (int64, error) {
	if !utf8.Valid(text) {
		return 0, fmt.Errorf("save %s: text is not valid UTF-8", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, fmt.Errorf("resolve save path: %w", err)
	}
	if mode == 0 {
		mode = 0o644
	}
	if expectedModTime != 0 {
		changed, err := ChangedOnDisk(abs, expectedModTime)
		if err != nil {
			return 0, err
		}
		if changed {
			return 0, ErrChangedOnDisk
		}
	}
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, resolveErr := filepath.EvalSymlinks(abs)
		if resolveErr != nil {
			return 0, fmt.Errorf("resolve save symlink: %w", resolveErr)
		}
		abs = resolved
	}
	data := encodeNewlines(text, newline)
	if err := writeAtomic(abs, data, mode); err != nil {
		return 0, fmt.Errorf("save %s: %w", abs, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return 0, fmt.Errorf("inspect saved file: %w", err)
	}
	return info.ModTime().UnixNano(), nil
}

// ChangedOnDisk reports whether path has changed since modTime.
func ChangedOnDisk(path string, modTime int64) (bool, error) {
	if path == "" || modTime == 0 {
		return false, nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}
	return info.ModTime().UnixNano() != modTime, nil
}

func detectNewline(data []byte) Newline {
	if bytes.Contains(data, []byte("\r\n")) {
		return NewlineCRLF
	}
	if bytes.Contains(data, []byte("\r")) {
		return NewlineCR
	}
	return NewlineLF
}

func normalizeNewlines(data []byte) []byte {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	return bytes.ReplaceAll(data, []byte("\r"), []byte("\n"))
}

func encodeNewlines(data []byte, newline Newline) []byte {
	switch newline {
	case NewlineCRLF:
		return bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n"))
	case NewlineCR:
		return bytes.ReplaceAll(data, []byte("\n"), []byte("\r"))
	default:
		return data
	}
}

func writeAtomic(path string, data []byte, mode os.FileMode) (returnErr error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".e-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			if err := tmp.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close temporary file during cleanup: %w", err))
			}
		}
		if !renamed {
			if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary file: %w", err))
			}
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("set mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write content: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync content: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close content: %w", err)
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace destination: %w", err)
	}
	renamed = true
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open parent directory: %w", err)
	}
	if err := dirFile.Sync(); err != nil {
		if closeErr := dirFile.Close(); closeErr != nil {
			return fmt.Errorf("sync parent directory: %w (close: %v)", err, closeErr)
		}
		return fmt.Errorf("sync parent directory: %w", err)
	}
	if err := dirFile.Close(); err != nil {
		return fmt.Errorf("close parent directory: %w", err)
	}
	return nil
}

// PrepareSave applies EditorConfig-style whitespace rules without mutating the
// caller's byte slice.
func PrepareSave(text []byte, trimTrailing, finalNewline bool) []byte {
	out := append([]byte(nil), text...)
	if trimTrailing {
		lines := strings.Split(string(out), "\n")
		for i := range lines {
			lines[i] = strings.TrimRight(lines[i], " \t")
		}
		out = []byte(strings.Join(lines, "\n"))
	}
	if finalNewline && len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out
}
