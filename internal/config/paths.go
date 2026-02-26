package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// ConfigDir returns the mcpl configuration directory.
// Respects MCPL_CONFIG_DIR override.
func ConfigDir() (string, error) {
	if dir := os.Getenv("MCPL_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}
	return filepath.Join(base, "mcpl"), nil
}

// SocketPath returns the path to the daemon's Unix socket.
// macOS: $TMPDIR/mcpl-$UID/mcpl.sock
// Linux: $XDG_RUNTIME_DIR/mcpl/mcpl.sock
func SocketPath() (string, error) {
	uid := strconv.Itoa(os.Getuid())
	var dir string

	if runtime.GOOS == "darwin" {
		tmpDir := os.TempDir()
		dir = filepath.Join(tmpDir, "mcpl-"+uid)
	} else {
		xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
		if xdgRuntime == "" {
			xdgRuntime = filepath.Join(os.TempDir(), "mcpl-"+uid)
		}
		dir = filepath.Join(xdgRuntime, "mcpl")
	}
	return filepath.Join(dir, "mcpl.sock"), nil
}

// LogDir returns the directory for mcpl log files.
func LogDir() (string, error) {
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("log dir: %w", err)
		}
		return filepath.Join(home, "Library", "Logs", "mcpl"), nil
	}
	cfgDir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "logs"), nil
}

// ConfigFilePath returns the path to config.json.
func ConfigFilePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// PIDFilePath returns the path to the daemon PID file.
func PIDFilePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mcpl.pid"), nil
}

// LockFilePath returns the path to the flock file for atomic daemon start.
func LockFilePath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "mcpl.lock"), nil
}

// ReadDaemonPID reads and parses the PID from the daemon PID file.
// Returns the PID and PID file path. Returns an error if the PID file
// does not exist or contains an invalid PID.
func ReadDaemonPID() (int, string, error) {
	pidPath, err := PIDFilePath()
	if err != nil {
		return 0, "", fmt.Errorf("determine PID path: %w", err)
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, pidPath, fmt.Errorf("read PID file: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, pidPath, fmt.Errorf("invalid PID file: %w", err)
	}
	return pid, pidPath, nil
}
