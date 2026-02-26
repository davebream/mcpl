package cmd

import (
	"fmt"
	"os"
	"syscall"

	"github.com/davebream/mcpl/internal/config"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the mcpl daemon",
	RunE: func(cmd *cobra.Command, args []string) error {
		pid, _, err := config.ReadDaemonPID()
		if err != nil {
			return fmt.Errorf("daemon not running (%v)", err)
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("find process: %w", err)
		}

		if err := process.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("send SIGTERM: %w (daemon may not be running)", err)
		}

		fmt.Println("Sent shutdown signal to daemon")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
