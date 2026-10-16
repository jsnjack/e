package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFilesSkipsRepositoryMetadata(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"README.md", "cmd/e/main.go", ".git/config"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := Files(context.Background(), root, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"README.md", filepath.Join("cmd", "e", "main.go")}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("Files() = %v, want %v", files, want)
	}
}

func TestFilesHonorsLimitAndCancellation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := Files(context.Background(), root, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("limited files = %v", files)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Files(cancelled, root, 10); err == nil {
		t.Fatal("cancelled discovery returned no error")
	}
}

func TestFilesUsesGitIgnore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	for name, body := range map[string]string{
		".gitignore": "build/\n", "main.go": "package main", "build/out.bin": "x",
		"node_modules/pkg/index.js": "x",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	files, err := Files(context.Background(), root, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".gitignore", "main.go", filepath.Join("node_modules", "pkg", "index.js")}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("Files() = %v, want %v", files, want)
	}
}

func TestListDirFollowsLinksAndSorts(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"beta", "Alpha"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "beta"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	entries, err := ListDir(root, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{"Alpha", true}, {"beta", true}, {"link", true}, {"a.txt", false}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("ListDir() = %v, want %v", entries, want)
	}
}
