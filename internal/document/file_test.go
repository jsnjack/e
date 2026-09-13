package document

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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
			if _, err := Save(path, file.Text, file.Newline, file.Mode); err != nil {
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
	if _, err := SaveChecked(path, []byte("editor"), NewlineLF, 0o600, opened.ModTime); !errors.Is(err, ErrChangedOnDisk) {
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
	if _, err := Save(link, []byte("after"), NewlineLF, 0o600); err != nil {
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
	}{
		{"unchanged", "a  ", false, false, "a  "},
		{"both", "a  \n b\t", true, true, "a\n b\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(PrepareSave([]byte(tc.in), tc.trim, tc.line)); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
