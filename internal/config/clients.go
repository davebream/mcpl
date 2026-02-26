package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ClientInfo describes a detected MCP client configuration file.
type ClientInfo struct {
	Name    string                   // Human-readable client name (e.g. "Claude Code")
	Path    string                   // Absolute path to config file
	Servers map[string]*ServerConfig // Parsed MCP server definitions
}

// DetectClients detects all MCP client configs using the real home directory.
func DetectClients() []ClientInfo {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return DetectClientsIn(home)
}

// DetectClientsIn detects MCP client configs relative to the given home directory.
func DetectClientsIn(home string) []ClientInfo {
	var clients []ClientInfo

	candidates := []struct {
		name string
		path string
	}{
		{"Claude Code", filepath.Join(home, ".claude.json")},
		{"Cursor", filepath.Join(home, ".cursor", "mcp.json")},
	}

	if runtime.GOOS == "darwin" {
		candidates = append(candidates, struct {
			name string
			path string
		}{
			"Claude Desktop",
			filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"),
		})
	}

	for _, c := range candidates {
		servers, err := parseClientConfig(c.path)
		if err != nil || len(servers) == 0 {
			continue
		}
		clients = append(clients, ClientInfo{
			Name:    c.name,
			Path:    c.path,
			Servers: servers,
		})
	}

	return clients
}

// parseClientConfig reads an MCP client config file and extracts mcpServers.
func parseClientConfig(path string) (map[string]*ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	serversJSON, ok := raw["mcpServers"]
	if !ok {
		return nil, nil
	}

	var servers map[string]*ServerConfig
	if err := json.Unmarshal(serversJSON, &servers); err != nil {
		return nil, err
	}

	return servers, nil
}

// RewriteClientConfig rewrites a single server entry in a client config file
// to use the mcpl shim instead of the original command.
func RewriteClientConfig(path, serverName, mcplBin string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read client config: %w", err)
	}

	// Parse as generic JSON to preserve all fields
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse client config: %w", err)
	}

	serversJSON, ok := raw["mcpServers"]
	if !ok {
		return fmt.Errorf("no mcpServers in %s", path)
	}

	var servers map[string]json.RawMessage
	if err := json.Unmarshal(serversJSON, &servers); err != nil {
		return fmt.Errorf("parse mcpServers: %w", err)
	}

	if _, exists := servers[serverName]; !exists {
		return fmt.Errorf("server %q not found in %s", serverName, path)
	}

	// Replace the server entry with mcpl shim
	shimEntry := map[string]interface{}{
		"command": mcplBin,
		"args":    []string{"connect", serverName},
	}
	shimJSON, _ := json.Marshal(shimEntry)
	servers[serverName] = shimJSON

	// Reassemble
	newServersJSON, _ := json.Marshal(servers)
	raw["mcpServers"] = newServersJSON

	output, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	output = append(output, '\n')

	return os.WriteFile(path, output, 0600)
}

// RewriteAllServers rewrites all server entries in a client config to use mcpl shims.
func RewriteAllServers(path, mcplBin string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read client config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse client config: %w", err)
	}

	serversJSON, ok := raw["mcpServers"]
	if !ok {
		return nil // nothing to rewrite
	}

	var servers map[string]json.RawMessage
	if err := json.Unmarshal(serversJSON, &servers); err != nil {
		return fmt.Errorf("parse mcpServers: %w", err)
	}

	for name := range servers {
		shimEntry := map[string]interface{}{
			"command": mcplBin,
			"args":    []string{"connect", name},
		}
		shimJSON, _ := json.Marshal(shimEntry)
		servers[name] = shimJSON
	}

	newServersJSON, _ := json.Marshal(servers)
	raw["mcpServers"] = newServersJSON

	output, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	output = append(output, '\n')

	return os.WriteFile(path, output, 0600)
}

// IsAlreadyMCPL checks if a server entry already points to mcpl.
func IsAlreadyMCPL(sc *ServerConfig) bool {
	if sc == nil {
		return false
	}
	base := filepath.Base(sc.Command)
	return base == "mcpl" && len(sc.Args) >= 2 && sc.Args[0] == "connect"
}
