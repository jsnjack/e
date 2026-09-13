package document

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const sessionFilename = "session.json"

// Snapshot is the state needed to restore one editor tab.
type Snapshot struct {
	ID       string  `json:"id"`
	Path     string  `json:"path,omitempty"`
	Title    string  `json:"title"`
	Text     []byte  `json:"-"`
	Modified bool    `json:"modified"`
	Cursor   int     `json:"cursor"`
	Newline  Newline `json:"newline"`
}

type sessionTab struct {
	ID       string  `json:"id"`
	Path     string  `json:"path,omitempty"`
	Title    string  `json:"title"`
	Recovery string  `json:"recovery,omitempty"`
	Modified bool    `json:"modified"`
	Cursor   int     `json:"cursor"`
	Newline  Newline `json:"newline"`
}

// SaveSession persists open tabs and recovery copies for unsaved content.
func SaveSession(stateDir string, snapshots []Snapshot) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	recoveryDir := filepath.Join(stateDir, "recovery")
	if err := os.MkdirAll(recoveryDir, 0o700); err != nil {
		return fmt.Errorf("create recovery directory: %w", err)
	}
	wanted := make(map[string]bool)
	tabs := make([]sessionTab, 0, len(snapshots))
	for _, snapshot := range snapshots {
		tab := sessionTab{ID: snapshot.ID, Path: snapshot.Path, Title: snapshot.Title, Modified: snapshot.Modified, Cursor: snapshot.Cursor, Newline: snapshot.Newline}
		if snapshot.Modified || snapshot.Path == "" {
			tab.Recovery = filepath.Join("recovery", snapshot.ID+".txt")
			fullPath := filepath.Join(stateDir, tab.Recovery)
			if err := writeAtomic(fullPath, snapshot.Text, 0o600); err != nil {
				return fmt.Errorf("save recovery for %s: %w", snapshot.Title, err)
			}
			wanted[fullPath] = true
		}
		tabs = append(tabs, tab)
	}
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
	body, err := json.Marshal(tabs)
	if err != nil {
		return fmt.Errorf("encode session: %w", err)
	}
	if err := writeAtomic(filepath.Join(stateDir, sessionFilename), body, 0o600); err != nil {
		return fmt.Errorf("save session: %w", err)
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
		snapshot := Snapshot{ID: tab.ID, Path: tab.Path, Title: tab.Title, Modified: tab.Modified, Cursor: tab.Cursor, Newline: tab.Newline}
		if tab.Recovery != "" {
			recoveryPath := filepath.Join(stateDir, filepath.Clean(tab.Recovery))
			if filepath.Dir(recoveryPath) != filepath.Join(stateDir, "recovery") {
				return nil, fmt.Errorf("invalid recovery path for %s", tab.Title)
			}
			snapshot.Text, err = os.ReadFile(recoveryPath)
			if err != nil {
				return nil, fmt.Errorf("read recovery for %s: %w", tab.Title, err)
			}
		}
		out = append(out, snapshot)
	}
	return out, nil
}
