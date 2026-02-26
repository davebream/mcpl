package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAtomicWriteFile(t *testing.T) {
	t.Run("writes file with correct permissions", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.json")

		err := AtomicWriteFile(path, []byte("hello"), 0600)
		require.NoError(t, err)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	})

	t.Run("refuses to write to symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "target.json")
		link := filepath.Join(dir, "link.json")

		os.WriteFile(target, []byte("original"), 0600)
		os.Symlink(target, link)

		err := AtomicWriteFile(link, []byte("modified"), 0600)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "symlink")
	})

	t.Run("creates parent directory", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "subdir", "test.json")

		err := AtomicWriteFile(path, []byte("hello"), 0600)
		require.NoError(t, err)

		data, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(data))
	})
}

func TestEnsureDir(t *testing.T) {
	t.Run("creates directory with 0700", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "new-dir")

		err := EnsureDir(dir, 0700)
		require.NoError(t, err)

		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.True(t, info.IsDir())
		assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
	})

	t.Run("succeeds if directory exists", func(t *testing.T) {
		dir := t.TempDir()
		err := EnsureDir(dir, 0700)
		assert.NoError(t, err)
	})
}
