package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davebream/mcpl/internal/config"
	"github.com/davebream/mcpl/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSocketPath creates a socket path inside a short 0700 directory to stay under macOS 104-byte limit.
func testSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mcpl-t-*")
	require.NoError(t, err)
	require.NoError(t, os.Chmod(dir, 0700))
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "t.sock")
}

// dialRetry attempts to connect to a Unix socket with retries.
func dialRetry(t *testing.T, socketPath string) net.Conn {
	t.Helper()
	var conn net.Conn
	var err error
	for i := 0; i < 20; i++ {
		conn, err = net.Dial("unix", socketPath)
		if err == nil {
			return conn
		}
		time.Sleep(25 * time.Millisecond)
	}
	require.NoError(t, err, "failed to connect to daemon socket after retries")
	return nil
}

func TestDaemonAcceptsConnections(t *testing.T) {
	socketPath := testSocketPath(t)
	cfg := &config.Config{
		IdleTimeout:       "30m",
		ServerIdleTimeout: "10m",
		Servers: map[string]*config.ServerConfig{
			"test-server": {Command: "/bin/cat", Args: []string{}},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := New(cfg, socketPath, nil)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Check if Run() failed immediately
	select {
	case err := <-errCh:
		require.NoError(t, err, "daemon Run() failed")
	case <-time.After(200 * time.Millisecond):
		// Good — daemon is running
	}

	conn := dialRetry(t, socketPath)
	defer conn.Close()

	req := &protocol.ConnectRequest{MCPL: 1, Type: "connect", Server: "test-server"}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	_, err = conn.Write(data)
	require.NoError(t, err)

	scanner := bufio.NewScanner(conn)
	require.True(t, scanner.Scan())

	var resp protocol.ConnectResponse
	err = json.Unmarshal(scanner.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "connected", resp.Type)
}

func TestDaemonRejectsUnknownServer(t *testing.T) {
	socketPath := testSocketPath(t)
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d, err := New(cfg, socketPath, nil)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	conn := dialRetry(t, socketPath)
	defer conn.Close()

	req := &protocol.ConnectRequest{MCPL: 1, Type: "connect", Server: "nonexistent"}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	conn.Write(data)

	scanner := bufio.NewScanner(conn)
	require.True(t, scanner.Scan())

	var resp protocol.ConnectResponse
	json.Unmarshal(scanner.Bytes(), &resp)
	assert.Equal(t, "error", resp.Type)
	assert.Equal(t, "unknown_server", resp.Code)
}

func TestDaemonSkipsUnmanagedServers(t *testing.T) {
	f := false
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"managed":   {Command: "/bin/cat"},
			"unmanaged": {Command: "npx", Args: []string{"-y", "@playwright/mcp"}, Managed: &f},
		},
	}

	socketPath := testSocketPath(t)
	d, err := New(cfg, socketPath, nil)
	require.NoError(t, err)

	// Managed server should be in the server map
	assert.Contains(t, d.servers, "managed")
	// Unmanaged server should NOT be in the server map
	assert.NotContains(t, d.servers, "unmanaged")
}
