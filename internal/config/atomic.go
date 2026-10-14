package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jsnjack/e/internal/document"
)

// WriteFileAtomic creates path's parent directory and durably replaces path
// through document.WriteFileAtomic.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	if err := document.WriteFileAtomic(path, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
