// Package config owns E's persistent settings and XDG paths.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// Paths contains the application-owned XDG locations.
type Paths struct {
	ConfigFile string
	StateDir   string
	CacheDir   string
}

// DefaultPaths resolves E's XDG paths.
func DefaultPaths() (Paths, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve config directory: %w", err)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve cache directory: %w", err)
	}
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, fmt.Errorf("resolve home directory: %w", err)
		}
		dataDir = filepath.Join(home, ".local", "share")
	}
	return Paths{
		ConfigFile: filepath.Join(configDir, "e", "config.toml"),
		StateDir:   filepath.Join(dataDir, "e"),
		CacheDir:   filepath.Join(cacheDir, "e"),
	}, nil
}
