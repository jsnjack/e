package document

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := []Snapshot{{ID: "one", Title: "Draft", Text: []byte("hello"), Modified: true, Cursor: 3, Newline: NewlineLF, Active: true}}
	if err := SaveSession(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0].Text) != "hello" || got[0].Cursor != 3 || !got[0].Modified || !got[0].Active {
		t.Fatalf("got %+v", got)
	}
}

func TestSessionRejectsRecoveryTraversal(t *testing.T) {
	dir := t.TempDir()
	body := `[{"id":"bad","title":"Bad","recovery":"recovery/../../outside.txt","modified":true,"newline":"lf"}]`
	if err := os.WriteFile(filepath.Join(dir, sessionFilename), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSession(dir); err == nil {
		t.Fatal("LoadSession accepted recovery path traversal")
	}
}

func TestSessionPersistsModTime(t *testing.T) {
	dir := t.TempDir()
	want := []Snapshot{{ID: "one", Path: "/tmp/a.txt", Title: "a.txt", Newline: NewlineLF, ModTime: 12345}}
	if err := SaveSession(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ModTime != 12345 {
		t.Fatalf("got %+v", got)
	}
}

func TestSessionSkipsMissingRecovery(t *testing.T) {
	dir := t.TempDir()
	snapshots := []Snapshot{
		{ID: "lost", Title: "Lost", Text: []byte("gone"), Modified: true, Newline: NewlineLF},
		{ID: "kept", Title: "Kept", Text: []byte("here"), Modified: true, Newline: NewlineLF},
	}
	if err := SaveSession(dir, snapshots); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "recovery", "lost.txt")); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "kept" || string(got[0].Text) != "here" {
		t.Fatalf("got %+v", got)
	}
}

func TestSaveSessionRemovesStaleRecovery(t *testing.T) {
	dir := t.TempDir()
	if err := SaveSession(dir, []Snapshot{{ID: "old", Title: "Old", Text: []byte("x"), Modified: true}}); err != nil {
		t.Fatal(err)
	}
	if err := SaveSession(dir, []Snapshot{{ID: "new", Title: "New", Text: []byte("y"), Modified: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "recovery", "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale recovery still present: %v", err)
	}
	got, err := LoadSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("got %+v", got)
	}
}
