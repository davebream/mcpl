package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectClientsIn(t *testing.T) {
	home := t.TempDir()

	// Create Claude Code config
	claudeConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"test": map[string]interface{}{
				"command": "npx",
				"args":    []interface{}{"-y", "test-server"},
			},
		},
	}
	data, _ := json.MarshalIndent(claudeConfig, "", "  ")
	os.WriteFile(filepath.Join(home, ".claude.json"), data, 0600)

	clients := DetectClientsIn(home)
	assert.NotEmpty(t, clients)
	assert.Equal(t, "Claude Code", clients[0].Name)
	assert.Len(t, clients[0].Servers, 1)
	assert.Equal(t, "npx", clients[0].Servers["test"].Command)
}

func TestDetectClientsIn_NoConfigs(t *testing.T) {
	home := t.TempDir()
	clients := DetectClientsIn(home)
	assert.Empty(t, clients)
}

func TestDetectClientsIn_Cursor(t *testing.T) {
	home := t.TempDir()

	cursorDir := filepath.Join(home, ".cursor")
	os.MkdirAll(cursorDir, 0700)
	cursorConfig := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"cursor-server": map[string]interface{}{
				"command": "node",
				"args":    []interface{}{"server.js"},
			},
		},
	}
	data, _ := json.MarshalIndent(cursorConfig, "", "  ")
	os.WriteFile(filepath.Join(cursorDir, "mcp.json"), data, 0600)

	clients := DetectClientsIn(home)
	assert.Len(t, clients, 1)
	assert.Equal(t, "Cursor", clients[0].Name)
}

func TestRewriteClientConfig(t *testing.T) {
	t.Run("rewrites server to mcpl shim", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")

		original := map[string]interface{}{
			"mcpServers": map[string]interface{}{
				"context7": map[string]interface{}{
					"command": "npx",
					"args":    []interface{}{"-y", "@upstash/context7-mcp"},
				},
			},
		}
		data, _ := json.MarshalIndent(original, "", "  ")
		os.WriteFile(path, data, 0600)

		err := RewriteClientConfig(path, "context7", "/usr/local/bin/mcpl")
		require.NoError(t, err)

		rewritten, _ := os.ReadFile(path)
		var result map[string]interface{}
		json.Unmarshal(rewritten, &result)

		servers := result["mcpServers"].(map[string]interface{})
		server := servers["context7"].(map[string]interface{})
		assert.Equal(t, "/usr/local/bin/mcpl", server["command"])

		args := server["args"].([]interface{})
		assert.Equal(t, "connect", args[0])
		assert.Equal(t, "context7", args[1])
	})

	t.Run("preserves other fields", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")

		original := map[string]interface{}{
			"mcpServers": map[string]interface{}{
				"a": map[string]interface{}{"command": "npx", "args": []interface{}{"a"}},
				"b": map[string]interface{}{"command": "npx", "args": []interface{}{"b"}},
			},
			"otherSetting": true,
		}
		data, _ := json.MarshalIndent(original, "", "  ")
		os.WriteFile(path, data, 0600)

		err := RewriteClientConfig(path, "a", "/usr/local/bin/mcpl")
		require.NoError(t, err)

		rewritten, _ := os.ReadFile(path)
		var result map[string]interface{}
		json.Unmarshal(rewritten, &result)

		// Other setting preserved
		assert.Equal(t, true, result["otherSetting"])

		// Server "b" unchanged
		servers := result["mcpServers"].(map[string]interface{})
		b := servers["b"].(map[string]interface{})
		assert.Equal(t, "npx", b["command"])
	})

	t.Run("returns error for missing server", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")

		original := map[string]interface{}{
			"mcpServers": map[string]interface{}{},
		}
		data, _ := json.MarshalIndent(original, "", "  ")
		os.WriteFile(path, data, 0600)

		err := RewriteClientConfig(path, "nonexistent", "/usr/local/bin/mcpl")
		assert.Error(t, err)
	})
}

func TestRewriteAllServers(t *testing.T) {
	t.Run("all managed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")

		original := map[string]interface{}{
			"mcpServers": map[string]interface{}{
				"a": map[string]interface{}{"command": "npx", "args": []interface{}{"a-server"}},
				"b": map[string]interface{}{"command": "node", "args": []interface{}{"b-server.js"}},
			},
			"otherSetting": "preserved",
		}
		data, _ := json.MarshalIndent(original, "", "  ")
		os.WriteFile(path, data, 0600)

		mcplServers := map[string]*ServerConfig{
			"a": {Command: "npx", Args: []string{"a-server"}},
			"b": {Command: "node", Args: []string{"b-server.js"}},
		}

		err := RewriteAllServers(path, "/usr/local/bin/mcpl", mcplServers)
		require.NoError(t, err)

		rewritten, _ := os.ReadFile(path)
		var result map[string]interface{}
		json.Unmarshal(rewritten, &result)

		assert.Equal(t, "preserved", result["otherSetting"])

		servers := result["mcpServers"].(map[string]interface{})
		for _, name := range []string{"a", "b"} {
			server := servers[name].(map[string]interface{})
			assert.Equal(t, "/usr/local/bin/mcpl", server["command"])
			args := server["args"].([]interface{})
			assert.Equal(t, "connect", args[0])
			assert.Equal(t, name, args[1])
		}
	})

	t.Run("mixed managed and unmanaged", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.json")

		original := map[string]interface{}{
			"mcpServers": map[string]interface{}{
				"context7":   map[string]interface{}{"command": "npx", "args": []interface{}{"-y", "@upstash/context7-mcp"}},
				"playwright": map[string]interface{}{"command": "npx", "args": []interface{}{"-y", "@playwright/mcp"}},
			},
		}
		data, _ := json.MarshalIndent(original, "", "  ")
		os.WriteFile(path, data, 0600)

		f := false
		mcplServers := map[string]*ServerConfig{
			"context7":   {Command: "npx", Args: []string{"-y", "@upstash/context7-mcp"}},
			"playwright": {Command: "npx", Args: []string{"-y", "@playwright/mcp"}, Managed: &f},
		}

		err := RewriteAllServers(path, "/usr/local/bin/mcpl", mcplServers)
		require.NoError(t, err)

		rewritten, _ := os.ReadFile(path)
		var result map[string]interface{}
		json.Unmarshal(rewritten, &result)

		servers := result["mcpServers"].(map[string]interface{})

		// Managed server gets shim
		ctx7 := servers["context7"].(map[string]interface{})
		assert.Equal(t, "/usr/local/bin/mcpl", ctx7["command"])

		// Unmanaged server gets direct command
		pw := servers["playwright"].(map[string]interface{})
		assert.Equal(t, "npx", pw["command"])
		args := pw["args"].([]interface{})
		assert.Equal(t, "-y", args[0])
		assert.Equal(t, "@playwright/mcp", args[1])
	})
}

func TestIsManaged(t *testing.T) {
	// nil Managed = default managed
	assert.True(t, (&ServerConfig{Command: "npx"}).IsManaged())

	// explicit true
	tr := true
	assert.True(t, (&ServerConfig{Command: "npx", Managed: &tr}).IsManaged())

	// explicit false
	f := false
	assert.False(t, (&ServerConfig{Command: "npx", Managed: &f}).IsManaged())
}

func TestAddServerEntry_Unmanaged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := map[string]interface{}{
		"mcpServers": map[string]interface{}{},
	}
	data, _ := json.MarshalIndent(original, "", "  ")
	os.WriteFile(path, data, 0600)

	f := false
	sc := &ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "@playwright/mcp"},
		Managed: &f,
	}

	err := AddServerEntry(path, "playwright", "/usr/local/bin/mcpl", sc)
	require.NoError(t, err)

	rewritten, _ := os.ReadFile(path)
	var result map[string]interface{}
	json.Unmarshal(rewritten, &result)

	servers := result["mcpServers"].(map[string]interface{})
	pw := servers["playwright"].(map[string]interface{})
	// Unmanaged: should use direct command, not mcpl shim
	assert.Equal(t, "npx", pw["command"])
	args := pw["args"].([]interface{})
	assert.Equal(t, "-y", args[0])
	assert.Equal(t, "@playwright/mcp", args[1])
}

func TestAddServerEntry_Managed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := map[string]interface{}{
		"mcpServers": map[string]interface{}{},
	}
	data, _ := json.MarshalIndent(original, "", "  ")
	os.WriteFile(path, data, 0600)

	sc := &ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "@upstash/context7-mcp"},
	}

	err := AddServerEntry(path, "context7", "/usr/local/bin/mcpl", sc)
	require.NoError(t, err)

	rewritten, _ := os.ReadFile(path)
	var result map[string]interface{}
	json.Unmarshal(rewritten, &result)

	servers := result["mcpServers"].(map[string]interface{})
	ctx7 := servers["context7"].(map[string]interface{})
	// Managed: should use mcpl shim
	assert.Equal(t, "/usr/local/bin/mcpl", ctx7["command"])
	args := ctx7["args"].([]interface{})
	assert.Equal(t, "connect", args[0])
	assert.Equal(t, "context7", args[1])
}

func TestIsAlreadyMCPL(t *testing.T) {
	assert.True(t, IsAlreadyMCPL(&ServerConfig{Command: "/usr/local/bin/mcpl", Args: []string{"connect", "test"}}))
	assert.True(t, IsAlreadyMCPL(&ServerConfig{Command: "mcpl", Args: []string{"connect", "test"}}))
	assert.False(t, IsAlreadyMCPL(&ServerConfig{Command: "npx", Args: []string{"-y", "test"}}))
	assert.False(t, IsAlreadyMCPL(nil))
}
