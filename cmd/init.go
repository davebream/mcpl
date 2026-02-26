package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/davebream/mcpl/internal/config"
	"github.com/spf13/cobra"
)

var (
	initDiff    bool
	initApply   bool
	initRestore bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Detect MCP servers and create mcpl config",
	Long: `Scans Claude Code, Claude Desktop, and Cursor configs for MCP server
definitions, merges them into a single mcpl config, and rewrites client
configs to use mcpl shims.

Use --diff to preview changes without modifying anything.
Use --apply to create the config and rewrite client configs.
Use --restore to revert client configs from backups.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if initRestore {
			return runRestore()
		}

		clients := config.DetectClients()
		if len(clients) == 0 {
			fmt.Println("No MCP client configs found.")
			return nil
		}

		for _, c := range clients {
			fmt.Printf("Found %s: %s (%d servers)\n", c.Name, c.Path, len(c.Servers))
		}

		merged, conflicts := config.MergeClientsServers(clients)

		if len(conflicts) > 0 {
			fmt.Println("\nConflicts (same name, different config):")
			for _, c := range conflicts {
				fmt.Printf("  %s — defined differently in %s and %s\n", c.Name, c.Sources[0], c.Sources[1])
			}
			fmt.Println("Resolve conflicts manually or use 'mcpl add' for individual servers.")
		}

		if len(merged) == 0 {
			fmt.Println("\nNo new servers to import (all already use mcpl).")
			return nil
		}

		fmt.Printf("\nServers to import: %d\n", len(merged))
		for name, sc := range merged {
			if !sc.IsManaged() {
				fmt.Printf("  %s: %s %v (unmanaged)\n", name, sc.Command, sc.Args)
			} else {
				fmt.Printf("  %s: %s %v\n", name, sc.Command, sc.Args)
			}
		}

		if initDiff {
			fmt.Println("\n(Dry run — no changes made. Use --apply to proceed.)")
			return nil
		}

		if !initApply {
			fmt.Println("\nUse --diff to preview or --apply to proceed.")
			return nil
		}

		// Create mcpl config
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

		cfg := config.DefaultConfig()
		for name, sc := range merged {
			cfg.Servers[name] = sc
		}
		if err := cfg.Save(cfgPath); err != nil {
			return fmt.Errorf("save config: %w", err)
		}
		fmt.Printf("\nCreated %s\n", cfgPath)

		// Find mcpl binary path
		mcplBin, err := exec.LookPath("mcpl")
		if err != nil {
			mcplBin, _ = os.Executable()
		}

		// Backup and rewrite client configs
		for _, c := range clients {
			if err := config.BackupClientConfig(c.Path); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not backup %s: %v\n", c.Path, err)
				continue
			}
			if err := config.RewriteAllServers(c.Path, mcplBin, cfg.Servers); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not rewrite %s: %v\n", c.Path, err)
				continue
			}
			fmt.Printf("Modified %s (backup at %s.mcpl.bak)\n", c.Path, c.Path)
		}

		fmt.Println("\nDone. Run 'mcpl status' to check the daemon.")
		return nil
	},
}

func runRestore() error {
	clients := config.DetectClients()
	if len(clients) == 0 {
		// Even without detected clients, try common paths
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}

		// Try to restore any .mcpl.bak files we can find
		restored := 0
		for _, path := range commonConfigPaths(home) {
			if err := config.RestoreClientConfig(path); err == nil {
				fmt.Printf("Restored %s\n", path)
				restored++
			}
		}
		if restored == 0 {
			fmt.Println("No backups found to restore.")
		}
		return nil
	}

	for _, c := range clients {
		if err := config.RestoreClientConfig(c.Path); err != nil {
			fmt.Printf("Skip %s: %v\n", c.Path, err)
		} else {
			fmt.Printf("Restored %s\n", c.Path)
		}
	}
	return nil
}

func commonConfigPaths(home string) []string {
	return []string{
		home + "/.claude.json",
		home + "/.cursor/mcp.json",
		home + "/Library/Application Support/Claude/claude_desktop_config.json",
	}
}

func init() {
	initCmd.Flags().BoolVar(&initDiff, "diff", false, "Preview changes without modifying anything")
	initCmd.Flags().BoolVar(&initApply, "apply", false, "Apply changes (create config, rewrite client configs)")
	initCmd.Flags().BoolVar(&initRestore, "restore", false, "Restore client configs from backups")
	rootCmd.AddCommand(initCmd)
}
