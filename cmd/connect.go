package cmd

import (
	"fmt"
	"os"

	"github.com/davebream/mcpl/internal/config"
	"github.com/davebream/mcpl/internal/shim"
	"github.com/spf13/cobra"
)

var connectCmd = &cobra.Command{
	Use:   "connect <server-name>",
	Short: "Connect to an MCP server through the daemon",
	Long:  "Stdio shim that bridges MCP protocol between a client and the daemon. Auto-starts daemon if needed.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serverName := args[0]

		socketPath, err := config.SocketPath()
		if err != nil {
			return fmt.Errorf("socket path: %w", err)
		}

		lockPath, err := config.LockFilePath()
		if err != nil {
			return fmt.Errorf("lock path: %w", err)
		}

		cfgDir, err := config.ConfigDir()
		if err != nil {
			return fmt.Errorf("config dir: %w", err)
		}

		if err := shim.EnsureDaemon(socketPath, lockPath, cfgDir); err != nil {
			fmt.Fprintf(os.Stderr, "mcpl: failed to start daemon: %v\n", err)
			os.Exit(1)
		}

		if err := shim.Connect(serverName, socketPath); err != nil {
			fmt.Fprintf(os.Stderr, "mcpl: %v\n", err)
			os.Exit(1)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(connectCmd)
}
