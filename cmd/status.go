package cmd

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/davebream/mcpl/internal/config"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE: func(cmd *cobra.Command, args []string) error {
		pidPath, err := config.PIDFilePath()
		if err != nil {
			return err
		}

		data, err := os.ReadFile(pidPath)
		if err != nil {
			fmt.Println("Daemon: not running")
			return nil
		}

		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			fmt.Println("Daemon: not running (invalid PID file)")
			return nil
		}

		// Check if process is alive
		process, err := os.FindProcess(pid)
		if err != nil {
			fmt.Printf("Daemon: not running (PID %d not found)\n", pid)
			return nil
		}

		if err := process.Signal(syscall.Signal(0)); err != nil {
			fmt.Printf("Daemon: not running (PID %d, stale PID file)\n", pid)
			return nil
		}

		// Try socket connection
		socketPath, err := config.SocketPath()
		if err != nil {
			fmt.Printf("Daemon: running (PID %d), socket path unknown\n", pid)
			return nil
		}

		conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
		if err != nil {
			fmt.Printf("Daemon: running (PID %d), socket not reachable\n", pid)
			return nil
		}
		conn.Close()

		fmt.Printf("Daemon: running (PID %d)\n", pid)
		fmt.Printf("Socket: %s\n", socketPath)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
