package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/davebream/mcpl/internal/config"
	"github.com/spf13/cobra"
)

var logsFollow bool

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show daemon logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		logDir, err := config.LogDir()
		if err != nil {
			return err
		}

		logFile := filepath.Join(logDir, "daemon.log")
		if _, err := os.Stat(logFile); os.IsNotExist(err) {
			fmt.Println("No log file found at", logFile)
			return nil
		}

		if logsFollow {
			// Use tail -f for follow mode
			tailCmd := exec.Command("tail", "-f", logFile)
			tailCmd.Stdout = os.Stdout
			tailCmd.Stderr = os.Stderr
			return tailCmd.Run()
		}

		// Show last 50 lines
		tailCmd := exec.Command("tail", "-n", "50", logFile)
		tailCmd.Stdout = os.Stdout
		tailCmd.Stderr = os.Stderr
		return tailCmd.Run()
	},
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "Follow log output")
	rootCmd.AddCommand(logsCmd)
}
