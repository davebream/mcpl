package config

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigDir(t *testing.T) {
	t.Run("uses MCPL_CONFIG_DIR override", func(t *testing.T) {
		t.Setenv("MCPL_CONFIG_DIR", "/tmp/mcpl-test-config")
		dir, err := ConfigDir()
		require.NoError(t, err)
		assert.Equal(t, "/tmp/mcpl-test-config", dir)
	})

	t.Run("returns platform default when no override", func(t *testing.T) {
		t.Setenv("MCPL_CONFIG_DIR", "")
		dir, err := ConfigDir()
		require.NoError(t, err)
		assert.NotEmpty(t, dir)
		if runtime.GOOS == "darwin" {
			assert.Contains(t, dir, "Application Support/mcpl")
		}
	})
}

func TestSocketPath(t *testing.T) {
	path, err := SocketPath()
	require.NoError(t, err)
	assert.Contains(t, path, "mcpl.sock")
	assert.Contains(t, filepath.Base(filepath.Dir(path)), "mcpl-")
}

func TestLogDir(t *testing.T) {
	t.Run("returns platform default", func(t *testing.T) {
		dir, err := LogDir()
		require.NoError(t, err)
		assert.NotEmpty(t, dir)
		if runtime.GOOS == "darwin" {
			assert.Contains(t, dir, "Logs/mcpl")
		}
	})
}

func TestPIDFilePath(t *testing.T) {
	t.Setenv("MCPL_CONFIG_DIR", "/tmp/mcpl-test")
	path, err := PIDFilePath()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/mcpl-test/mcpl.pid", path)
}

func TestLockFilePath(t *testing.T) {
	t.Setenv("MCPL_CONFIG_DIR", "/tmp/mcpl-test")
	path, err := LockFilePath()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/mcpl-test/mcpl.lock", path)
}

func TestConfigFilePath(t *testing.T) {
	t.Setenv("MCPL_CONFIG_DIR", "/tmp/mcpl-test")
	path, err := ConfigFilePath()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/mcpl-test/config.json", path)
}
