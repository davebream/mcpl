package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path atomically using temp file + rename.
// Refuses to write if path is a symlink.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	// Check for symlink
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write: %s is a symlink", path)
		}
	}

	dir := filepath.Dir(path)
	if err := EnsureDir(dir, 0700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".mcpl-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()

	defer func() {
		tmp.Close()
		os.Remove(tmpPath) // clean up on error
	}()

	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp to target: %w", err)
	}

	return nil
}

// EnsureDir creates a directory with the given permissions if it doesn't exist.
func EnsureDir(dir string, perm os.FileMode) error {
	if err := os.MkdirAll(dir, perm); err != nil {
		return fmt.Errorf("ensure dir %s: %w", dir, err)
	}
	return nil
}
