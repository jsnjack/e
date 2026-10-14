package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeProjectTargetRejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	w := window{projectRoot: root}
	if !w.safeProjectTarget(filepath.Join(root, "new.txt")) {
		t.Fatal("ordinary project child was rejected")
	}
	if w.safeProjectTarget(filepath.Join(root, "..", "outside.txt")) {
		t.Fatal("lexical project escape was accepted")
	}
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if w.safeProjectTarget(filepath.Join(link, "escaped.txt")) {
		t.Fatal("symlink project escape was accepted")
	}
}

func TestRenameProjectPathDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	destination := filepath.Join(root, "destination.txt")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("destination"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := renameProjectPath(source, destination); !os.IsExist(err) {
		t.Fatalf("rename error = %v, want file exists", err)
	}
	for path, want := range map[string]string{source: "source", destination: "destination"} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q, %v", path, data, err)
		}
	}
	renamed := filepath.Join(root, "renamed.txt")
	if err := renameProjectPath(source, renamed); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(renamed); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRootFor(t *testing.T) {
	repos := map[string]bool{"/home/user/repo/.git": true}
	exists := func(path string) bool { return repos[path] }
	tests := []struct{ file, want string }{
		{"/home/user/repo/cmd/main.go", "/home/user/repo"},
		{"/home/user/repo/main.go", "/home/user/repo"},
		{"/home/user/notes/todo.md", "/home/user/notes"},
		{"/todo.md", "/"},
	}
	for _, test := range tests {
		if got := projectRootFor(test.file, exists); got != test.want {
			t.Fatalf("projectRootFor(%q) = %q, want %q", test.file, got, test.want)
		}
	}
}
