package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/davebream/mcpl/internal/config"
	"github.com/spf13/cobra"
)

var addJSON bool

var addCmd = &cobra.Command{
	Use:   "add <name> <command> [args...]",
	Short: "Add an MCP server",
	Long: `Add an MCP server to mcpl config and all detected client configs.

Examples:
  mcpl add context7 npx -y @upstash/context7-mcp
  echo '{"command":"npx","args":["-y","server"]}' | mcpl add myserver --json -`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		var sc *config.ServerConfig

		if addJSON {
			// Read JSON from remaining args or stdin
			var data []byte
			var err error

			if len(args) >= 2 && args[1] == "-" {
				data, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20)) // 1 MB limit
			} else if len(args) >= 2 {
				data = []byte(args[1])
			} else {
				return fmt.Errorf("--json requires a JSON string or '-' for stdin")
			}

			if err != nil {
				return fmt.Errorf("read input: %w", err)
			}

			sc = &config.ServerConfig{}
			if err := json.Unmarshal(data, sc); err != nil {
				return fmt.Errorf("parse JSON: %w", err)
			}
		} else {
			if len(args) < 2 {
				return fmt.Errorf("usage: mcpl add <name> <command> [args...]")
			}
			sc = &config.ServerConfig{
				Command: args[1],
				Args:    args[2:],
			}
		}

		// Load or create mcpl config
		cfgPath, err := config.ConfigFilePath()
		if err != nil {
			return err
		}

		cfgDir, err := config.ConfigDir()
		if err != nil {
			return err
		}
		if err := config.EnsureDir(cfgDir, 0700); err != nil {
			return err
		}

		cfg, err := config.Load(cfgPath)
		if err != nil {
			cfg = config.DefaultConfig()
		}

		if _, exists := cfg.Servers[name]; exists {
			return fmt.Errorf("server %q already exists. Use 'mcpl remove %s' first", name, name)
		}

		cfg.Servers[name] = sc
		if err := cfg.Save(cfgPath); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("Added %s to %s\n", name, cfgPath)

		// Update client configs
		mcplBin, err := exec.LookPath("mcpl")
		if err != nil {
			mcplBin, _ = os.Executable()
		}

		clients := config.DetectClients()
		for _, c := range clients {
			if err := config.RewriteClientConfig(c.Path, name, mcplBin); err != nil {
				// Server may not exist in this client — that's fine, add it
				if addServerToClient(c.Path, name, mcplBin) == nil {
					fmt.Printf("Added shim to %s\n", c.Path)
				}
			} else {
				fmt.Printf("Updated %s\n", c.Path)
			}
		}

		return nil
	},
}

// addServerToClient adds a new mcpl shim entry to a client config file.
func addServerToClient(path, serverName, mcplBin string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	var servers map[string]json.RawMessage
	if serversJSON, ok := raw["mcpServers"]; ok {
		if err := json.Unmarshal(serversJSON, &servers); err != nil {
			return err
		}
	} else {
		servers = make(map[string]json.RawMessage)
	}

	shimEntry := map[string]interface{}{
		"command": mcplBin,
		"args":    []string{"connect", serverName},
	}
	shimJSON, err := json.Marshal(shimEntry)
	if err != nil {
		return err
	}
	servers[serverName] = shimJSON

	newServersJSON, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	raw["mcpServers"] = newServersJSON

	output, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	output = append(output, '\n')

	return config.AtomicWriteFile(path, output, 0600)
}

func init() {
	addCmd.Flags().BoolVar(&addJSON, "json", false, "Parse server config from JSON")
	rootCmd.AddCommand(addCmd)
}
