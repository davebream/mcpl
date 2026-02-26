package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeServers(t *testing.T) {
	t.Run("deduplicates identical configs", func(t *testing.T) {
		a := map[string]*ServerConfig{
			"context7": {Command: "npx", Args: []string{"-y", "context7"}},
		}
		b := map[string]*ServerConfig{
			"context7": {Command: "npx", Args: []string{"-y", "context7"}},
		}
		merged, conflicts := MergeServers(a, b)
		assert.Len(t, merged, 1)
		assert.Empty(t, conflicts)
	})

	t.Run("merges different servers", func(t *testing.T) {
		a := map[string]*ServerConfig{
			"server-a": {Command: "npx", Args: []string{"a"}},
		}
		b := map[string]*ServerConfig{
			"server-b": {Command: "npx", Args: []string{"b"}},
		}
		merged, conflicts := MergeServers(a, b)
		assert.Len(t, merged, 2)
		assert.Empty(t, conflicts)
	})

	t.Run("detects conflicts", func(t *testing.T) {
		a := map[string]*ServerConfig{
			"test": {Command: "npx", Args: []string{"v1"}},
		}
		b := map[string]*ServerConfig{
			"test": {Command: "npx", Args: []string{"v2"}},
		}
		_, conflicts := MergeServers(a, b)
		assert.Len(t, conflicts, 1)
		assert.Equal(t, "test", conflicts[0].Name)
	})
}

func TestMergeClientsServers(t *testing.T) {
	t.Run("skips servers already using mcpl", func(t *testing.T) {
		clients := []ClientInfo{
			{
				Name: "Claude Code",
				Servers: map[string]*ServerConfig{
					"already": {Command: "mcpl", Args: []string{"connect", "already"}},
					"real":    {Command: "npx", Args: []string{"-y", "real-server"}},
				},
			},
		}
		merged, conflicts := MergeClientsServers(clients)
		assert.Len(t, merged, 1)
		assert.Contains(t, merged, "real")
		assert.Empty(t, conflicts)
	})

	t.Run("detects cross-client conflicts", func(t *testing.T) {
		clients := []ClientInfo{
			{
				Name: "Claude Code",
				Servers: map[string]*ServerConfig{
					"context7": {Command: "npx", Args: []string{"-y", "context7-v1"}},
				},
			},
			{
				Name: "Cursor",
				Servers: map[string]*ServerConfig{
					"context7": {Command: "npx", Args: []string{"-y", "context7-v2"}},
				},
			},
		}
		merged, conflicts := MergeClientsServers(clients)
		assert.Len(t, merged, 1) // first one wins
		assert.Len(t, conflicts, 1)
		assert.Equal(t, "context7", conflicts[0].Name)
		assert.Equal(t, "Claude Code", conflicts[0].Sources[0])
		assert.Equal(t, "Cursor", conflicts[0].Sources[1])
	})
}

func TestBackupAndRestore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := []byte(`{"mcpServers":{}}`)
	os.WriteFile(path, original, 0600)

	// Backup
	err := BackupClientConfig(path)
	require.NoError(t, err)

	bakPath := path + ".mcpl.bak"
	_, err = os.Stat(bakPath)
	assert.NoError(t, err)

	// Modify original
	os.WriteFile(path, []byte(`{"modified":true}`), 0600)

	// Restore
	err = RestoreClientConfig(path)
	require.NoError(t, err)

	restored, _ := os.ReadFile(path)
	assert.Equal(t, original, restored)

	// Backup file removed
	_, err = os.Stat(bakPath)
	assert.True(t, os.IsNotExist(err))
}

func TestRestoreNoBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(`{}`), 0600)

	err := RestoreClientConfig(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no backup found")
}
