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

func TestSessionDir(t *testing.T) {
	state := "/state"
	cases := []struct {
		name, first, second string
		same                bool
	}{
		{name: "same folder", first: "/home/a/project", second: "/home/a/project", same: true},
		{name: "same name in other folders", first: "/home/a/project", second: "/home/b/project"},
		{name: "folder and no folder", first: "/home/a/project", second: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, second := SessionDir(state, tc.first), SessionDir(state, tc.second)
			if (first == second) != tc.same {
				t.Fatalf("SessionDir(%q) = %s, SessionDir(%q) = %s", tc.first, first, tc.second, second)
			}
			if filepath.Dir(first) != filepath.Join(state, sessionsDirname) {
				t.Fatalf("SessionDir(%q) = %s is outside the sessions directory", tc.first, first)
			}
		})
	}
	if got := filepath.Base(SessionDir(state, "/home/a/my project")); got[:len("my_project-")] != "my_project-" {
		t.Fatalf("session directory name = %q", got)
	}
}

func TestLastSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got, err := LoadLastSession(dir); err != nil || got != (LastSession{}) {
		t.Fatalf("LoadLastSession before any save = %+v, %v", got, err)
	}
	want := LastSession{Folder: "/home/a/project", Project: true}
	if err := SaveLastSession(dir, want); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadLastSession(dir); err != nil || got != want {
		t.Fatalf("LoadLastSession = %+v, %v", got, err)
	}
}

func TestMigrateSharedSession(t *testing.T) {
	dir := t.TempDir()
	shared := []Snapshot{
		{ID: "draft", Title: "Untitled", Text: []byte("note"), Modified: true, Active: true},
		{ID: "empty", Title: "Untitled"},
		{ID: "edited", Path: "/tmp/edited.txt", Title: "edited.txt", Text: []byte("new"), Modified: true},
		{ID: "clean", Path: "/tmp/clean.txt", Title: "clean.txt"},
	}
	if err := SaveSession(dir, shared); err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if err := MigrateSharedSession(dir); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	for _, name := range []string{sessionFilename, recoveryDirname} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("shared %s still present: %v", name, err)
		}
	}
	got, err := LoadSession(SessionDir(dir, ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "draft" || string(got[0].Text) != "note" || got[0].Active ||
		got[1].ID != "edited" || string(got[1].Text) != "new" {
		t.Fatalf("migrated tabs = %+v", got)
	}
}
