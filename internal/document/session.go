package document

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	sessionFilename     = "session.json"
	recoveryDirname     = "recovery"
	sessionsDirname     = "sessions"
	lastSessionFilename = "last.json"
	// noFolderSession holds the tabs of a window without a folder. Folder
	// session names always end in a hash, so they cannot collide with it.
	noFolderSession = "none"
)

// SessionDir is the state directory for the tabs of folder. An empty folder
// names the session of a window without one.
func SessionDir(stateDir, folder string) string {
	if folder == "" {
		return filepath.Join(stateDir, sessionsDirname, noFolderSession)
	}
	sum := sha256.Sum256([]byte(folder))
	name := strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, filepath.Base(folder))
	return filepath.Join(stateDir, sessionsDirname, name+"-"+hex.EncodeToString(sum[:8]))
}

// LastSession names the folder whose tabs were saved most recently, so E
// started without arguments reopens it.
type LastSession struct {
	Folder string `json:"folder,omitempty"`
	// Project is set when the folder was open as a project, not only the
	// folder of the files in its tabs.
	Project bool `json:"project,omitempty"`
}

// SaveLastSession records the folder whose tabs were saved most recently.
func SaveLastSession(stateDir string, last LastSession) error {
	dir := filepath.Join(stateDir, sessionsDirname)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create sessions directory: %w", err)
	}
	body, err := json.Marshal(last)
	if err != nil {
		return fmt.Errorf("encode last session: %w", err)
	}
	if err := writeAtomic(filepath.Join(dir, lastSessionFilename), body, 0o600); err != nil {
		return fmt.Errorf("save last session: %w", err)
	}
	return nil
}

// LoadLastSession returns the folder whose tabs were saved most recently. It
// is empty when no session has been saved yet.
func LoadLastSession(stateDir string) (LastSession, error) {
	var last LastSession
	body, err := os.ReadFile(filepath.Join(stateDir, sessionsDirname, lastSessionFilename))
	if errors.Is(err, os.ErrNotExist) {
		return last, nil
	}
	if err != nil {
		return last, fmt.Errorf("read last session: %w", err)
	}
	if err := json.Unmarshal(body, &last); err != nil {
		return LastSession{}, fmt.Errorf("decode last session: %w", err)
	}
	return last, nil
}

// MigrateSharedSession replaces the single session that older versions kept
// for every folder. Its unsaved text moves to the session without a folder;
// its tabs without unsaved text are dropped.
func MigrateSharedSession(stateDir string) error {
	legacy := filepath.Join(stateDir, sessionFilename)
	if _, err := os.Stat(legacy); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect shared session: %w", err)
	}
	snapshots, err := LoadSession(stateDir)
	if err != nil {
		return fmt.Errorf("read shared session: %w", err)
	}
	target := SessionDir(stateDir, "")
	// A migration interrupted after this save runs again; skip what it moved.
	kept, err := LoadSession(target)
	if err != nil {
		return fmt.Errorf("read session without a folder: %w", err)
	}
	moved := make(map[string]bool, len(kept))
	for _, snapshot := range kept {
		moved[snapshot.ID] = true
	}
	for _, snapshot := range snapshots {
		unsaved := snapshot.Modified && snapshot.Path != "" || snapshot.Path == "" && len(snapshot.Text) > 0
		if unsaved && !moved[snapshot.ID] {
			snapshot.Active = false
			kept = append(kept, snapshot)
		}
	}
	if err := SaveSession(target, kept); err != nil {
		return fmt.Errorf("move unsaved tabs: %w", err)
	}
	if err := os.Remove(legacy); err != nil {
		return fmt.Errorf("remove shared session: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(stateDir, recoveryDirname)); err != nil {
		return fmt.Errorf("remove shared recovery copies: %w", err)
	}
	return nil
}

// Snapshot is the state needed to restore one editor tab.
type Snapshot struct {
	ID       string  `json:"id"`
	Path     string  `json:"path,omitempty"`
	Title    string  `json:"title"`
	Text     []byte  `json:"-"`
	Modified bool    `json:"modified"`
	Cursor   int     `json:"cursor"`
	Newline  Newline `json:"newline"`
	Active   bool    `json:"active,omitempty"`
	ModTime  int64   `json:"mod_time,omitempty"`
}

type sessionTab struct {
	ID       string  `json:"id"`
	Path     string  `json:"path,omitempty"`
	Title    string  `json:"title"`
	Recovery string  `json:"recovery,omitempty"`
	Modified bool    `json:"modified"`
	Cursor   int     `json:"cursor"`
	Newline  Newline `json:"newline"`
	Active   bool    `json:"active,omitempty"`
	ModTime  int64   `json:"mod_time,omitempty"`
}

// SaveSession persists open tabs and recovery copies for unsaved content.
func SaveSession(stateDir string, snapshots []Snapshot) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	recoveryDir := filepath.Join(stateDir, recoveryDirname)
	if err := os.MkdirAll(recoveryDir, 0o700); err != nil {
		return fmt.Errorf("create recovery directory: %w", err)
	}
	wanted := make(map[string]bool)
	tabs := make([]sessionTab, 0, len(snapshots))
	for _, snapshot := range snapshots {
		tab := sessionTab{
			ID: snapshot.ID, Path: snapshot.Path, Title: snapshot.Title,
			Modified: snapshot.Modified, Cursor: snapshot.Cursor,
			Newline: snapshot.Newline, Active: snapshot.Active,
			ModTime: snapshot.ModTime,
		}
		if snapshot.Modified || snapshot.Path == "" {
			tab.Recovery = filepath.Join(recoveryDirname, snapshot.ID+".txt")
			fullPath := filepath.Join(stateDir, tab.Recovery)
			if err := writeAtomic(fullPath, snapshot.Text, 0o600); err != nil {
				return fmt.Errorf("save recovery for %s: %w", snapshot.Title, err)
			}
			wanted[fullPath] = true
		}
		tabs = append(tabs, tab)
	}
	body, err := json.Marshal(tabs)
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	if err := writeAtomic(filepath.Join(stateDir, sessionFilename), body, 0o600); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	// Remove stale recovery files only after session.json stops naming them.
	entries, err := os.ReadDir(recoveryDir)
	if err != nil {
		return fmt.Errorf("list recovery directory: %w", err)
	}
	for _, entry := range entries {
		path := filepath.Join(recoveryDir, entry.Name())
		if !wanted[path] {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove stale recovery %s: %w", path, err)
			}
		}
	}
	return nil
}

// LoadSession returns restorable tab metadata and recovery content.
func LoadSession(stateDir string) ([]Snapshot, error) {
	body, err := os.ReadFile(filepath.Join(stateDir, sessionFilename))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	var tabs []sessionTab
	if err := json.Unmarshal(body, &tabs); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	out := make([]Snapshot, 0, len(tabs))
	for _, tab := range tabs {
		snapshot := Snapshot{
			ID: tab.ID, Path: tab.Path, Title: tab.Title,
			Modified: tab.Modified, Cursor: tab.Cursor,
			Newline: tab.Newline, Active: tab.Active,
			ModTime: tab.ModTime,
		}
		if tab.Recovery != "" {
			recoveryPath := filepath.Join(stateDir, filepath.Clean(tab.Recovery))
			if filepath.Dir(recoveryPath) != filepath.Join(stateDir, recoveryDirname) {
				return nil, fmt.Errorf("invalid recovery path for %s", tab.Title)
			}
			snapshot.Text, err = os.ReadFile(recoveryPath)
			if errors.Is(err, os.ErrNotExist) {
				slog.Warn("skip session tab with missing recovery file", "title", tab.Title, "path", recoveryPath)
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read recovery for %s: %w", tab.Title, err)
			}
		}
		out = append(out, snapshot)
	}
	return out, nil
}
