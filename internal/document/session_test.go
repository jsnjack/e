package document

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := []Snapshot{{ID: "one", Title: "Draft", Text: []byte("hello"), Modified: true, Cursor: 3, Newline: NewlineLF}}
	if err := SaveSession(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0].Text) != "hello" || got[0].Cursor != 3 || !got[0].Modified {
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
