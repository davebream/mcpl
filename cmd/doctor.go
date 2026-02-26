package cmd

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/davebream/mcpl/internal/config"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check mcpl installation and configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		allOK := true

		// 1. Config file
		var cfg *config.Config
		cfgPath, err := config.ConfigFilePath()
		if err != nil {
			fmt.Printf("Config:  FAIL (cannot determine path: %v)\n", err)
			allOK = false
		} else {
			cfg, err = config.Load(cfgPath)
			if err != nil {
				fmt.Printf("Config:  FAIL (%v)\n", err)
				allOK = false
			} else {
				fmt.Printf("Config:  OK (%d servers, %s)\n", len(cfg.Servers), cfgPath)
			}
		}

		// 2. Socket
		socketPath, err := config.SocketPath()
		if err != nil {
			fmt.Printf("Socket:  FAIL (cannot determine path: %v)\n", err)
			allOK = false
		} else {
			info, err := os.Stat(socketPath)
			if err != nil {
				fmt.Printf("Socket:  WARN (not present at %s)\n", socketPath)
			} else {
				perm := info.Mode().Perm()
				if perm&0077 != 0 {
					fmt.Printf("Socket:  FAIL (insecure permissions %04o at %s)\n", perm, socketPath)
					allOK = false
				} else {
					fmt.Printf("Socket:  OK (%04o, %s)\n", perm, socketPath)
				}
			}
		}

		// 3. Daemon process
		pid, _, err := config.ReadDaemonPID()
		if err != nil {
			fmt.Println("Daemon:  WARN (no PID file, daemon may not be running)")
		} else {
			process, _ := os.FindProcess(pid)
			if process != nil && process.Signal(syscall.Signal(0)) == nil {
				fmt.Printf("Daemon:  OK (PID %d)\n", pid)
			} else {
				fmt.Printf("Daemon:  WARN (PID %d not running, stale PID file)\n", pid)
			}
		}

		// 4. Socket connectivity
		if socketPath != "" {
			conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
			if err != nil {
				fmt.Println("Connect: WARN (cannot connect to daemon socket)")
			} else {
				conn.Close()
				fmt.Println("Connect: OK")
			}
		}

		// 5. Server commands in PATH
		if cfg != nil {
			for name, scfg := range cfg.Servers {
				if _, err := exec.LookPath(scfg.Command); err != nil {
					fmt.Printf("Server %s: WARN (command %q not found in PATH)\n", name, scfg.Command)
				} else {
					fmt.Printf("Server %s: OK (%s)\n", name, scfg.Command)
				}
			}
		}

		if !allOK {
			return fmt.Errorf("some checks failed")
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}
