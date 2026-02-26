package config

import (
	"fmt"
	"os"
	"reflect"
)

// Conflict represents a name collision between two client configs.
type Conflict struct {
	Name    string
	Configs []*ServerConfig
	Sources []string // Client names (e.g. "Claude Code", "Cursor")
}

// MergeServers merges server configs from multiple sources.
// Identical configs are deduplicated. Different configs with the same name are conflicts.
func MergeServers(maps ...map[string]*ServerConfig) (map[string]*ServerConfig, []Conflict) {
	merged := make(map[string]*ServerConfig)
	sources := make(map[string]string) // name -> first source
	var conflicts []Conflict

	for i, m := range maps {
		source := fmt.Sprintf("source-%d", i)
		for name, cfg := range m {
			existing, exists := merged[name]
			if !exists {
				merged[name] = cfg
				sources[name] = source
				continue
			}

			if serverConfigsEqual(existing, cfg) {
				continue // identical, deduplicate
			}

			conflicts = append(conflicts, Conflict{
				Name:    name,
				Configs: []*ServerConfig{existing, cfg},
				Sources: []string{sources[name], source},
			})
		}
	}

	return merged, conflicts
}

// MergeClientsServers merges servers from ClientInfo slices.
func MergeClientsServers(clients []ClientInfo) (map[string]*ServerConfig, []Conflict) {
	merged := make(map[string]*ServerConfig)
	sources := make(map[string]string)
	var conflicts []Conflict

	for _, client := range clients {
		for name, cfg := range client.Servers {
			if IsAlreadyMCPL(cfg) {
				continue // skip servers already using mcpl
			}

			existing, exists := merged[name]
			if !exists {
				merged[name] = cfg
				sources[name] = client.Name
				continue
			}

			if serverConfigsEqual(existing, cfg) {
				continue
			}

			conflicts = append(conflicts, Conflict{
				Name:    name,
				Configs: []*ServerConfig{existing, cfg},
				Sources: []string{sources[name], client.Name},
			})
		}
	}

	return merged, conflicts
}

func serverConfigsEqual(a, b *ServerConfig) bool {
	return reflect.DeepEqual(a, b)
}

// BackupClientConfig creates a .bak copy of a client config.
func BackupClientConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path+".mcpl.bak", data, 0600)
}

// RestoreClientConfig restores a client config from its .bak copy.
func RestoreClientConfig(path string) error {
	bakPath := path + ".mcpl.bak"
	data, err := os.ReadFile(bakPath)
	if err != nil {
		return fmt.Errorf("no backup found at %s", bakPath)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return os.Remove(bakPath)
}
