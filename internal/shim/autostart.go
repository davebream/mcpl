package shim

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

func isStaleSocket(socketPath string) bool {
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		return false
	}
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err != nil {
		return true // exists but can't connect = stale
	}
	conn.Close()
	return false
}

// EnsureDaemon ensures the daemon is running. Uses flock for atomic startup.
func EnsureDaemon(socketPath, lockPath, configDir string) error {
	// Try connecting first (fast path — daemon already running)
	conn, err := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil
	}

	// Acquire flock
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDONLY, 0600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	// Under lock: try again (another shim may have started daemon)
	conn, err = net.DialTimeout("unix", socketPath, 200*time.Millisecond)
	if err == nil {
		conn.Close()
		return nil
	}

	// Clean stale socket
	if isStaleSocket(socketPath) {
		os.Remove(socketPath)
		pidPath := filepath.Join(configDir, "mcpl.pid")
		os.Remove(pidPath)
	}

	// Spawn daemon
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	cmd := exec.Command(exe, "daemon", "--foreground")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	// Release process — daemon runs independently
	cmd.Process.Release()

	// Poll for socket (50 x 10ms = 500ms)
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		conn, err := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return nil
		}
	}

	return fmt.Errorf("daemon failed to start within 500ms")
}
