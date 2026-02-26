package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/davebream/mcpl/internal/config"
	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the mcpl daemon",
	Long:  "Stops the daemon. The next 'mcpl connect' will auto-start a fresh daemon.",
	RunE: func(cmd *cobra.Command, args []string) error {
		pidPath, err := config.PIDFilePath()
		if err != nil {
			return err
		}

		data, err := os.ReadFile(pidPath)
		if err != nil {
			fmt.Println("Daemon not running, nothing to restart")
			return nil
		}

		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			return fmt.Errorf("invalid PID file: %w", err)
		}

		process, err := os.FindProcess(pid)
		if err != nil {
			return fmt.Errorf("find process: %w", err)
		}

		if err := process.Signal(syscall.SIGTERM); err != nil {
			fmt.Println("Daemon already stopped")
			return nil
		}

		// Wait for process to exit (up to 5s)
		stopped := false
		for i := 0; i < 50; i++ {
			time.Sleep(100 * time.Millisecond)
			if err := process.Signal(syscall.Signal(0)); err != nil {
				stopped = true
				break
			}
		}

		if stopped {
			fmt.Println("Daemon stopped. Next 'mcpl connect' will start a fresh daemon.")
		} else {
			fmt.Fprintf(os.Stderr, "Warning: daemon (PID %d) did not exit within 5s. It may still be running.\n", pid)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(restartCmd)
}
