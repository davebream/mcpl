package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/davebream/mcpl/internal/config"
	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an MCP server",
	Long:  "Remove a server from mcpl config and all detected client configs.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfgPath, err := config.ConfigFilePath()
		if err != nil {
			return err
		}

		cfg, err := config.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		if _, exists := cfg.Servers[name]; !exists {
			return fmt.Errorf("server %q not found in config", name)
		}

		delete(cfg.Servers, name)
		if err := cfg.Save(cfgPath); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Removed %s from %s\n", name, cfgPath)

		// Remove from client configs
		clients := config.DetectClients()
		for _, c := range clients {
			if err := removeServerFromClient(c.Path, name); err == nil {
				fmt.Printf("Removed from %s\n", c.Path)
			}
		}

		return nil
	},
}

// removeServerFromClient removes a server entry from a client config file.
func removeServerFromClient(path, serverName string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	serversJSON, ok := raw["mcpServers"]
	if !ok {
		return fmt.Errorf("no mcpServers in %s", path)
	}

	var servers map[string]json.RawMessage
	if err := json.Unmarshal(serversJSON, &servers); err != nil {
		return err
	}

	if _, exists := servers[serverName]; !exists {
		return fmt.Errorf("server %q not in %s", serverName, path)
	}

	delete(servers, serverName)

	newServersJSON, _ := json.Marshal(servers)
	raw["mcpServers"] = newServersJSON

	output, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	output = append(output, '\n')

	return os.WriteFile(path, output, 0600)
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
