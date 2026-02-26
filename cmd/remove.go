package cmd

import (
	"fmt"

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
			if err := config.RemoveServerEntry(c.Path, name); err == nil {
				fmt.Printf("Removed from %s\n", c.Path)
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
}
