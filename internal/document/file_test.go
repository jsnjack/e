package document

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenSave(t *testing.T) {
	tests := []struct {
		name string
		data string
		want string
		eol  Newline
	}{
		{"lf", "one\ntwo\n", "one\ntwo\n", NewlineLF},
		{"crlf", "one\r\ntwo\r\n", "one\ntwo\n", NewlineCRLF},
		{"cr", "one\rtwo\r", "one\ntwo\n", NewlineCR},
		{"lf with stray cr", "one\ntwo\rthree\nfour\n", "one\ntwo\rthree\nfour\n", NewlineLF},
		{"crlf majority with lone cr", "a\r\nb\rc\r\n", "a\nb\rc\n", NewlineCRLF},
		{"utf8 bom", "\xef\xbb\xbfone\r\ntwo\r\n", "one\ntwo\n", NewlineCRLF},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "note.txt")
			if err := os.WriteFile(path, []byte(tc.data), 0o640); err != nil {
				t.Fatal(err)
			}
			file, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(file.Text) != tc.want || file.Newline != tc.eol {
				t.Fatalf("got %q %q", file.Text, file.Newline)
			}
			if _, err := Save(path, file.Text, file.Newline, file.BOM, file.Mode); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.data {
				t.Fatalf("saved %q, want %q", got, tc.data)
			}
		})
	}
}

func TestOpenRejectsNonTextInputs(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		path string
		data []byte
		dir  bool
	}{
		{name: "nul byte", path: filepath.Join(root, "nul.txt"), data: []byte{'a', 0, 'b'}},
		{name: "invalid utf8", path: filepath.Join(root, "invalid.txt"), data: []byte{0xff}},
		{name: "directory", path: filepath.Join(root, "folder"), dir: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.dir {
				if err := os.Mkdir(test.path, 0o755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(test.path, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(test.path); err == nil {
				t.Fatal("Open accepted a non-text input")
			}
		})
	}
}

func TestChangedOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := ChangedOnDisk(path, file.ModTime)
	if err != nil || changed {
		t.Fatalf("unchanged file = %v, %v", changed, err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("after"), 100), 0o600); err != nil {
		t.Fatal(err)
	}
	changedAt := time.Unix(0, file.ModTime).Add(time.Second)
	if err := os.Chtimes(path, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	changed, err = ChangedOnDisk(path, file.ModTime)
	if err != nil || !changed {
		t.Fatalf("changed file = %v, %v", changed, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	changed, err = ChangedOnDisk(path, file.ModTime)
	if err != nil || !changed {
		t.Fatalf("removed file = %v, %v", changed, err)
	}
}

func TestSaveCheckedRejectsExternalChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	changedAt := time.Unix(0, opened.ModTime).Add(time.Second)
	if err := os.Chtimes(path, changedAt, changedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveChecked(path, []byte("editor"), NewlineLF, false, 0o600, opened.ModTime); !errors.Is(err, ErrChangedOnDisk) {
		t.Fatalf("SaveChecked error = %v, want ErrChangedOnDisk", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "external" {
		t.Fatalf("disk content = %q, %v", data, err)
	}
}

func TestSavePreservesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	link := filepath.Join(root, "link.txt")
	if err := os.WriteFile(target, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(link, []byte("after"), NewlineLF, false, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("save replaced symlink: mode=%v", info.Mode())
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "after" {
		t.Fatalf("target content = %q, %v", data, err)
	}
}

func TestPrepareSave(t *testing.T) {
	tests := []struct {
		name string
		in   string
		trim bool
		line bool
		want string
		same bool
	}{
		{"unchanged", "a  ", false, false, "a  ", true},
		{"final newline present", "a\n", false, true, "a\n", true},
		{"final newline added", "a", false, true, "a\n", false},
		{"empty", "", true, true, "", false},
		{"trim only", "a \n\tb\t\n c ", true, false, "a\n\tb\n c", false},
		{"both", "a  \n b\t", true, true, "a\n b\n", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := []byte(tc.in)
			got := PrepareSave(in, tc.trim, tc.line)
			if string(got) != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if string(in) != tc.in {
				t.Fatalf("input mutated to %q", in)
			}
			if tc.same && len(in) > 0 && &got[0] != &in[0] {
				t.Fatal("unchanged text was copied")
			}
		})
	}
}

func TestDetectNewline(t *testing.T) {
	tests := []struct {
		name string
		data string
		want Newline
	}{
		{"empty", "", NewlineLF},
		{"one crlf among lf", "a\nb\r\nc\n", NewlineLF},
		{"tie lf crlf", "a\nb\r\n", NewlineLF},
		{"tie crlf cr", "a\r\nb\r", NewlineCRLF},
		{"cr majority", "a\rb\rc\n", NewlineCR},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectNewline([]byte(tc.data)); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeNewlinesAvoidsCopy(t *testing.T) {
	data := []byte("one\ntwo\n")
	if got := normalizeNewlines(data, NewlineLF); &got[0] != &data[0] {
		t.Fatal("text without CR was copied")
	}
}

func TestSaveWritesBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	if _, err := Save(path, []byte("a\nb\n"), NewlineCRLF, true, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "\xef\xbb\xbfa\r\nb\r\n"; string(got) != want {
		t.Fatalf("saved %q, want %q", got, want)
	}
	file, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !file.BOM || string(file.Text) != "a\nb\n" {
		t.Fatalf("reopened BOM=%v text=%q", file.BOM, file.Text)
	}
}

func TestSaveMode(t *testing.T) {
	t.Run("new file uses umask", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "new.txt")
		if _, err := Save(path, []byte("x"), NewlineLF, false, 0); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if want := os.FileMode(0o666) &^ processUmask; info.Mode().Perm() != want {
			t.Fatalf("mode = %v, want %v", info.Mode().Perm(), want)
		}
	})
	t.Run("existing file keeps mode", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "old.txt")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := Save(path, []byte("y"), NewlineLF, false, 0); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Fatalf("mode = %v, want 0640", info.Mode().Perm())
		}
	})
	t.Run("setuid survives open and save", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "tool.sh")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o750|os.ModeSetuid); err != nil {
			t.Fatal(err)
		}
		file, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if file.Mode&os.ModeSetuid == 0 {
			t.Skip("file system does not keep setuid")
		}
		if _, err := Save(path, []byte("y"), NewlineLF, false, file.Mode); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode() & modeBits; got != 0o750|os.ModeSetuid {
			t.Fatalf("mode = %v, want %v", got, 0o750|os.ModeSetuid)
		}
	})
}

func TestDirSyncUnsupported(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"einval", &os.PathError{Op: "sync", Path: "/d", Err: syscall.EINVAL}, true},
		{"enotsup", &os.PathError{Op: "sync", Path: "/d", Err: syscall.ENOTSUP}, true},
		{"eio", &os.PathError{Op: "sync", Path: "/d", Err: syscall.EIO}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dirSyncUnsupported(tc.err); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
