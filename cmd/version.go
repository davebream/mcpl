package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
)

const protocolVersion = 1

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("mcpl %s (commit: %s, protocol: v%d)\n", version, commit, protocolVersion)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
