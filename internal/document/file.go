// Package document owns file metadata and durable disk operations.
package document

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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
// BOM records whether the file started with a UTF-8 byte order mark. Mode
// holds the permission bits plus setuid, setgid, and sticky.
type File struct {
	Path    string
	Text    []byte
	Newline Newline
	BOM     bool
	Mode    os.FileMode
	ModTime int64
}

// ErrChangedOnDisk indicates that a checked save would overwrite a newer disk version.
var ErrChangedOnDisk = errors.New("file changed on disk")

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// modeBits are the mode bits Open records and writeAtomic restores.
const modeBits = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky

// processUmask is read once at package init. syscall.Umask can only be read by
// setting it, and a set-and-restore while other threads create files would
// give those files the wrong mode. Package init runs before main starts any
// worker goroutines, so the short window here does not race with file I/O.
var processUmask os.FileMode

func init() {
	old := syscall.Umask(0)
	syscall.Umask(old)
	processUmask = os.FileMode(old) & os.ModePerm
}

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
	bom := bytes.HasPrefix(data, utf8BOM)
	if bom {
		data = data[len(utf8BOM):]
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return File{}, fmt.Errorf("open %s: file is not UTF-8 text", abs)
	}
	newline := detectNewline(data)
	data = normalizeNewlines(data, newline)
	return File{
		Path: abs, Text: data, Newline: newline, BOM: bom,
		Mode: info.Mode() & modeBits, ModTime: info.ModTime().UnixNano(),
	}, nil
}

// Save durably replaces path while preserving the requested line ending and
// byte order mark. A zero mode keeps the existing file's mode, or uses
// 0o666 minus the umask for a new file.
func Save(path string, text []byte, newline Newline, bom bool, mode os.FileMode) (int64, error) {
	return save(path, text, newline, bom, mode, 0)
}

// SaveChecked saves only when path still has expectedModTime.
func SaveChecked(path string, text []byte, newline Newline, bom bool, mode os.FileMode, expectedModTime int64) (int64, error) {
	return save(path, text, newline, bom, mode, expectedModTime)
}

func save(path string, text []byte, newline Newline, bom bool, mode os.FileMode, expectedModTime int64) (int64, error) {
	if !utf8.Valid(text) {
		return 0, fmt.Errorf("save %s: text is not valid UTF-8", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, fmt.Errorf("resolve save path: %w", err)
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
	data := encodeNewlines(text, newline, bom)
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

// detectNewline picks the most frequent line ending. Ties prefer LF, then
// CRLF, then CR, so a few stray bytes do not change the whole file.
func detectNewline(data []byte) Newline {
	var lf, crlf, cr int
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '\n':
			lf++
		case '\r':
			if i+1 < len(data) && data[i+1] == '\n' {
				crlf++
				i++
			} else {
				cr++
			}
		}
	}
	switch {
	case lf >= crlf && lf >= cr:
		return NewlineLF
	case crlf >= cr:
		return NewlineCRLF
	default:
		return NewlineCR
	}
}

// normalizeNewlines converts CRLF to LF. Lone CR bytes become LF only for a
// CR file; otherwise they stay in the text. Data without CR is returned as is.
func normalizeNewlines(data []byte, newline Newline) []byte {
	if bytes.IndexByte(data, '\r') < 0 {
		return data
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if newline == NewlineCR {
		data = bytes.ReplaceAll(data, []byte("\r"), []byte("\n"))
	}
	return data
}

// encodeNewlines converts LF text to newline with one allocation, or returns
// data unchanged for LF without a byte order mark.
func encodeNewlines(data []byte, newline Newline, bom bool) []byte {
	if newline != NewlineCRLF && newline != NewlineCR && !bom {
		return data
	}
	size := len(data)
	if newline == NewlineCRLF {
		size += bytes.Count(data, []byte("\n"))
	}
	if bom {
		size += len(utf8BOM)
	}
	out := make([]byte, 0, size)
	if bom {
		out = append(out, utf8BOM...)
	}
	switch newline {
	case NewlineCRLF:
		for {
			idx := bytes.IndexByte(data, '\n')
			if idx < 0 {
				break
			}
			out = append(out, data[:idx]...)
			out = append(out, '\r', '\n')
			data = data[idx+1:]
		}
		out = append(out, data...)
	case NewlineCR:
		start := len(out)
		out = append(out, data...)
		for i := start; i < len(out); i++ {
			if out[i] == '\n' {
				out[i] = '\r'
			}
		}
	default:
		out = append(out, data...)
	}
	return out
}

// WriteFileAtomic durably replaces path using a same-directory temporary file.
// A nonzero mode is applied as given, including setuid, setgid, and sticky.
// A zero mode keeps the replaced file's mode, or uses 0o666 minus the umask
// when path does not exist. The temporary file is given the replaced file's
// owner and group when the process is allowed to do so.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	return writeAtomic(path, data, mode)
}

func writeAtomic(path string, data []byte, mode os.FileMode) (returnErr error) {
	dir := filepath.Dir(path)
	original, err := os.Stat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect destination: %w", err)
	}
	switch {
	case mode != 0:
		mode &= modeBits
	case original != nil:
		mode = original.Mode() & modeBits
	default:
		mode = 0o666 &^ processUmask
	}
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
	if original != nil {
		if stat, ok := original.Sys().(*syscall.Stat_t); ok {
			if err := tmp.Chown(int(stat.Uid), int(stat.Gid)); err != nil && !errors.Is(err, syscall.EPERM) {
				return fmt.Errorf("set owner: %w", err)
			}
		}
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write content: %w", err)
	}
	// Chmod after Chown and Write: the kernel clears setuid and setgid on both.
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("set mode: %w", err)
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
	if err := dirFile.Sync(); err != nil && !dirSyncUnsupported(err) {
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

// dirSyncUnsupported reports whether a directory fsync error means the file
// system (for example FUSE or CIFS) cannot sync directories. The rename has
// already replaced the file, so such errors do not fail the save.
func dirSyncUnsupported(err error) bool {
	return errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP)
}

// PrepareSave applies EditorConfig-style whitespace rules without mutating the
// caller's byte slice. It returns text itself when nothing changes, so callers
// must treat the result as read-only.
func PrepareSave(text []byte, trimTrailing, finalNewline bool) []byte {
	needNewline := finalNewline && len(text) > 0 && text[len(text)-1] != '\n'
	if !trimTrailing {
		if !needNewline {
			return text
		}
		out := make([]byte, len(text), len(text)+1)
		copy(out, text)
		return append(out, '\n')
	}
	out := make([]byte, 0, len(text)+1)
	rest := text
	for {
		idx := bytes.IndexByte(rest, '\n')
		line := rest
		if idx >= 0 {
			line = rest[:idx]
		}
		out = append(out, bytes.TrimRight(line, " \t")...)
		if idx < 0 {
			break
		}
		out = append(out, '\n')
		rest = rest[idx+1:]
	}
	if finalNewline && len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out
}
