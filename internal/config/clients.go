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
		{"Claude Code (local)", filepath.Join(home, ".claude", "settings.local.json")},
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

// modifyClientServers reads a client config, applies a mutation to its mcpServers,
// and atomically writes back. The mutate function receives the current servers map
// and should modify it in place. If mutate returns an error, the file is not written.
func modifyClientServers(path string, mutate func(servers map[string]json.RawMessage) error) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read client config: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse client config: %w", err)
	}

	var servers map[string]json.RawMessage
	if serversJSON, ok := raw["mcpServers"]; ok {
		if err := json.Unmarshal(serversJSON, &servers); err != nil {
			return fmt.Errorf("parse mcpServers: %w", err)
		}
	} else {
		servers = make(map[string]json.RawMessage)
	}

	if err := mutate(servers); err != nil {
		return err
	}

	newServersJSON, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("marshal servers: %w", err)
	}
	raw["mcpServers"] = newServersJSON

	output, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	output = append(output, '\n')

	return AtomicWriteFile(path, output, 0600)
}

// marshalShimEntry returns the JSON for an mcpl shim server entry.
func marshalShimEntry(mcplBin, serverName string) (json.RawMessage, error) {
	return json.Marshal(map[string]interface{}{
		"command": mcplBin,
		"args":    []string{"connect", serverName},
	})
}

// marshalDirectEntry returns the JSON for a direct (unmanaged) server entry —
// the original command/args/env, bypassing the daemon.
func marshalDirectEntry(sc *ServerConfig) (json.RawMessage, error) {
	entry := map[string]interface{}{
		"command": sc.Command,
	}
	if len(sc.Args) > 0 {
		entry["args"] = sc.Args
	}
	if len(sc.Env) > 0 {
		entry["env"] = sc.Env
	}
	return json.Marshal(entry)
}

// marshalServerEntry returns the appropriate JSON entry for a server —
// shim for managed servers, direct command for unmanaged.
func marshalServerEntry(serverName, mcplBin string, sc *ServerConfig) (json.RawMessage, error) {
	if sc != nil && !sc.IsManaged() {
		return marshalDirectEntry(sc)
	}
	return marshalShimEntry(mcplBin, serverName)
}

// RewriteClientConfig rewrites a single server entry in a client config file
// to use the mcpl shim instead of the original command.
func RewriteClientConfig(path, serverName, mcplBin string) error {
	return modifyClientServers(path, func(servers map[string]json.RawMessage) error {
		if _, exists := servers[serverName]; !exists {
			return fmt.Errorf("server %q not found in %s", serverName, path)
		}
		shimJSON, err := marshalShimEntry(mcplBin, serverName)
		if err != nil {
			return err
		}
		servers[serverName] = shimJSON
		return nil
	})
}

// RewriteAllServers rewrites all server entries in a client config.
// Managed servers get shim entries; unmanaged servers get direct command entries.
func RewriteAllServers(path, mcplBin string, mcplServers map[string]*ServerConfig) error {
	return modifyClientServers(path, func(clientServers map[string]json.RawMessage) error {
		for name := range clientServers {
			sc := mcplServers[name] // may be nil for servers not in mcpl config
			entry, err := marshalServerEntry(name, mcplBin, sc)
			if err != nil {
				return err
			}
			clientServers[name] = entry
		}
		return nil
	})
}

// AddServerEntry adds a server entry to a client config file.
// Managed servers get shim entries; unmanaged servers get direct command entries.
func AddServerEntry(path, serverName, mcplBin string, sc *ServerConfig) error {
	return modifyClientServers(path, func(servers map[string]json.RawMessage) error {
		entry, err := marshalServerEntry(serverName, mcplBin, sc)
		if err != nil {
			return err
		}
		servers[serverName] = entry
		return nil
	})
}

// RemoveServerEntry removes a server entry from a client config file.
func RemoveServerEntry(path, serverName string) error {
	return modifyClientServers(path, func(servers map[string]json.RawMessage) error {
		if _, exists := servers[serverName]; !exists {
			return fmt.Errorf("server %q not in %s", serverName, path)
		}
		delete(servers, serverName)
		return nil
	})
}

// IsAlreadyMCPL checks if a server entry already points to mcpl.
func IsAlreadyMCPL(sc *ServerConfig) bool {
	if sc == nil {
		return false
	}
	base := filepath.Base(sc.Command)
	return base == "mcpl" && len(sc.Args) >= 2 && sc.Args[0] == "connect"
}
