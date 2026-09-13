package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic durably replaces path using a same-directory temporary file.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) (returnErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	closed := false
	renamed := false
	defer func() {
		if !closed {
			if err := tmp.Close(); err != nil {
				returnErr = errors.Join(returnErr, fmt.Errorf("close temporary file during cleanup: %w", err))
			}
		}
		if !renamed {
			if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
				returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary file: %w", err))
			}
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("set temporary file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	closed = true
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	renamed = true
	dirFile, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open parent directory: %w", err)
	}
	if err := dirFile.Sync(); err != nil {
		if closeErr := dirFile.Close(); closeErr != nil {
			return fmt.Errorf("sync parent directory: %w (close: %v)", err, closeErr)
		}
		return fmt.Errorf("sync parent directory: %w", err)
	}
	if err := dirFile.Close(); err != nil {
		return fmt.Errorf("close parent directory: %w", err)
	}
	return nil
}
