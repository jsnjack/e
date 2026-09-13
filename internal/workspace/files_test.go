package workspace

import (
	"context"
	"os"
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
