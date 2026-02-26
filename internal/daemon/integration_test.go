//go:build integration

package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/davebream/mcpl/internal/config"
	"github.com/davebream/mcpl/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildMockServer compiles the mock MCP server and returns the binary path.
func buildMockServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "mock_mcp_server")

	// Find the project root (relative from internal/daemon/)
	root, err := filepath.Abs("../../")
	require.NoError(t, err)

	src := filepath.Join(root, "testdata", "mock_mcp_server.go")
	require.FileExists(t, src, "mock MCP server source should exist")

	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run(), "building mock MCP server should succeed")

	return bin
}

// newTestDaemon creates a daemon with the given config on a temp socket.
// Uses a short path under /tmp to avoid macOS Unix socket path length limits (~104 chars).
func newTestDaemon(t *testing.T, cfg *config.Config) (*Daemon, string) {
	t.Helper()
	socketDir := fmt.Sprintf("/tmp/mcpl-t-%d", time.Now().UnixNano()%1000000)
	require.NoError(t, os.MkdirAll(socketDir, 0700))
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "t.sock")
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	d, err := New(cfg, socketPath, logger)
	require.NoError(t, err)
	return d, socketPath
}

// shimConn connects to the daemon socket and performs the mcpl handshake.
// Returns the connection and a buffered reader for reading responses.
func shimConn(t *testing.T, socketPath, serverName string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	require.NoError(t, err)

	// Send handshake
	req := &protocol.ConnectRequest{MCPL: 1, Type: "connect", Server: serverName}
	data, err := json.Marshal(req)
	require.NoError(t, err)
	data = append(data, '\n')
	_, err = conn.Write(data)
	require.NoError(t, err)

	reader := bufio.NewReader(conn)

	// Read handshake response
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)

	var resp protocol.ConnectResponse
	require.NoError(t, json.Unmarshal(line, &resp))
	require.Equal(t, "connected", resp.Type, "handshake should succeed, got: %+v", resp)

	return conn, reader
}

// sendJSON writes a JSON-RPC message to the connection.
func sendJSON(t *testing.T, conn net.Conn, v interface{}) {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	data = append(data, '\n')
	_, err = conn.Write(data)
	require.NoError(t, err)
}

// readJSON reads and parses a JSON-RPC message from the reader.
func readJSON(t *testing.T, reader *bufio.Reader) map[string]json.RawMessage {
	t.Helper()
	// Set a read deadline via the underlying conn if needed
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)

	var msg map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(line, &msg))
	return msg
}

func TestIntegration_HandshakeAndToolsList(t *testing.T) {
	mockBin := buildMockServer(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"mock": {
				Command: mockBin,
				Args:    []string{},
			},
		},
	}

	d, socketPath := newTestDaemon(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start daemon in background
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Wait for socket to be available
	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			return true
		}
		return false
	}, 2*time.Second, 50*time.Millisecond, "daemon should start listening")

	// Connect as shim
	conn, reader := shimConn(t, socketPath, "mock")
	defer conn.Close()

	// Give server a moment to start
	time.Sleep(200 * time.Millisecond)

	// Send initialize request
	sendJSON(t, conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]string{"name": "test-client", "version": "1.0"},
			"capabilities":    map[string]interface{}{},
		},
	})

	// Read response — the daemon forwards to mock server which responds
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp := readJSON(t, reader)
	assert.Contains(t, string(resp["result"]), "mock-server", "should get initialize response from mock server")

	// Send initialized notification
	sendJSON(t, conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialized",
	})

	// Send tools/list
	sendJSON(t, conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	})

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp = readJSON(t, reader)
	assert.Contains(t, string(resp["result"]), "echo", "should get tools list from mock server")

	// Verify response ID was remapped back to original
	assert.Equal(t, "2", string(resp["id"]), "response id should match original request id")

	cancel()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down in time")
	}
}

func TestIntegration_TwoShimsSameServer(t *testing.T) {
	mockBin := buildMockServer(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"mock": {
				Command: mockBin,
				Args:    []string{},
			},
		},
	}

	d, socketPath := newTestDaemon(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			return true
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)

	// Connect first shim
	conn1, reader1 := shimConn(t, socketPath, "mock")
	defer conn1.Close()

	time.Sleep(200 * time.Millisecond)

	// First shim sends initialize
	sendJSON(t, conn1, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]string{"name": "shim1"},
			"capabilities":    map[string]interface{}{},
		},
	})

	conn1.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp1 := readJSON(t, reader1)
	assert.Contains(t, string(resp1["result"]), "mock-server")

	// Connect second shim — server already running
	conn2, reader2 := shimConn(t, socketPath, "mock")
	defer conn2.Close()

	// Second shim sends initialize — should get cached response
	sendJSON(t, conn2, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]string{"name": "shim2"},
			"capabilities":    map[string]interface{}{},
		},
	})

	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp2 := readJSON(t, reader2)
	assert.Contains(t, string(resp2["result"]), "mock-server", "second shim should get initialize response (cached or forwarded)")

	// Both shims send tools/list with same local ID
	sendJSON(t, conn1, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	})
	sendJSON(t, conn2, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	})

	// Both should get correct responses (ID remapping prevents collision)
	conn1.SetReadDeadline(time.Now().Add(5 * time.Second))
	r1 := readJSON(t, reader1)
	assert.Contains(t, string(r1["result"]), "echo")
	assert.Equal(t, "2", string(r1["id"]), "shim1 response id should be 2")

	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	r2 := readJSON(t, reader2)
	assert.Contains(t, string(r2["result"]), "echo")
	assert.Equal(t, "2", string(r2["id"]), "shim2 response id should be 2")

	// Disconnect shim1
	conn1.Close()
	time.Sleep(100 * time.Millisecond)

	// Shim2 should still work
	sendJSON(t, conn2, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	})

	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	r3 := readJSON(t, reader2)
	assert.Contains(t, string(r3["result"]), "echo", "shim2 should still get responses after shim1 disconnects")

	cancel()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not shut down in time")
	}
}

func TestIntegration_UnknownServer(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	d, socketPath := newTestDaemon(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx)

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			return true
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)

	// Connect to non-existent server
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	require.NoError(t, err)
	defer conn.Close()

	req := &protocol.ConnectRequest{MCPL: 1, Type: "connect", Server: "nonexistent"}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	conn.Write(data)

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)

	var resp protocol.ConnectResponse
	require.NoError(t, json.Unmarshal(line, &resp))
	assert.Equal(t, "error", resp.Type)
	assert.Equal(t, "unknown_server", resp.Code)

	cancel()
}

func TestIntegration_ToolsCallEcho(t *testing.T) {
	mockBin := buildMockServer(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"mock": {
				Command: mockBin,
				Args:    []string{},
			},
		},
	}

	d, socketPath := newTestDaemon(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx)

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			return true
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)

	conn, reader := shimConn(t, socketPath, "mock")
	defer conn.Close()

	time.Sleep(200 * time.Millisecond)

	// Initialize
	sendJSON(t, conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]string{"name": "test"},
			"capabilities":    map[string]interface{}{},
		},
	})
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readJSON(t, reader) // consume initialize response

	// Send tools/call
	sendJSON(t, conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      "echo",
			"arguments": map[string]string{"message": "hello-integration"},
		},
	})

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	resp := readJSON(t, reader)
	resultStr := string(resp["result"])
	assert.Contains(t, resultStr, "hello-integration", "tools/call should echo back params")
	assert.Equal(t, "2", string(resp["id"]))

	cancel()
}

func TestIntegration_InvalidHandshake(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	d, socketPath := newTestDaemon(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx)

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			return true
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)

	t.Run("wrong protocol version", func(t *testing.T) {
		conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
		require.NoError(t, err)
		defer conn.Close()

		req := &protocol.ConnectRequest{MCPL: 99, Type: "connect", Server: "test"}
		data, _ := json.Marshal(req)
		data = append(data, '\n')
		conn.Write(data)

		reader := bufio.NewReader(conn)
		line, err := reader.ReadBytes('\n')
		require.NoError(t, err)

		var resp protocol.ConnectResponse
		require.NoError(t, json.Unmarshal(line, &resp))
		assert.Equal(t, "error", resp.Type)
		assert.Equal(t, "protocol_error", resp.Code)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
		require.NoError(t, err)
		defer conn.Close()

		conn.Write([]byte("not json\n"))

		reader := bufio.NewReader(conn)
		line, err := reader.ReadBytes('\n')
		require.NoError(t, err)

		var resp protocol.ConnectResponse
		require.NoError(t, json.Unmarshal(line, &resp))
		assert.Equal(t, "error", resp.Type)
		assert.Equal(t, "invalid_request", resp.Code)
	})

	cancel()
}

func TestIntegration_InitCaching(t *testing.T) {
	mockBin := buildMockServer(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"mock": {
				Command: mockBin,
				Args:    []string{},
			},
		},
	}

	d, socketPath := newTestDaemon(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx)

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			return true
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)

	// First shim — initialize goes to server
	conn1, reader1 := shimConn(t, socketPath, "mock")
	defer conn1.Close()
	time.Sleep(200 * time.Millisecond)

	sendJSON(t, conn1, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]string{"name": "shim1"},
			"capabilities":    map[string]interface{}{},
		},
	})

	conn1.SetReadDeadline(time.Now().Add(5 * time.Second))
	initResp := readJSON(t, reader1)
	require.Contains(t, string(initResp["result"]), "mock-server")

	// The init response should be cached now
	// Verify it's in the cache
	cached, ok := d.initCache.Get("mock")
	assert.True(t, ok, "initialize response should be cached")
	assert.NotNil(t, cached)

	// Second shim — should get cached response (never hits server)
	conn2, reader2 := shimConn(t, socketPath, "mock")
	defer conn2.Close()

	sendJSON(t, conn2, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      42,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]string{"name": "shim2"},
			"capabilities":    map[string]interface{}{},
		},
	})

	conn2.SetReadDeadline(time.Now().Add(5 * time.Second))
	initResp2 := readJSON(t, reader2)
	assert.Contains(t, string(initResp2["result"]), "mock-server", "should get cached init response")
	// The ID should match the requesting shim's ID (42), not the first shim's (1)
	assert.Equal(t, "42", string(initResp2["id"]), "cached response should use requesting shim's ID")

	cancel()
}

func TestIntegration_ConcurrentRequests(t *testing.T) {
	mockBin := buildMockServer(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"mock": {
				Command: mockBin,
				Args:    []string{},
			},
		},
	}

	d, socketPath := newTestDaemon(t, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go d.Run(ctx)

	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			conn.Close()
			return true
		}
		return false
	}, 2*time.Second, 50*time.Millisecond)

	conn, reader := shimConn(t, socketPath, "mock")
	defer conn.Close()
	time.Sleep(200 * time.Millisecond)

	// Initialize
	sendJSON(t, conn, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"clientInfo":      map[string]string{"name": "test"},
			"capabilities":    map[string]interface{}{},
		},
	})
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	readJSON(t, reader)

	// Send 10 concurrent requests with unique IDs
	for i := 2; i <= 11; i++ {
		sendJSON(t, conn, map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      i,
			"method":  "tools/call",
			"params": map[string]interface{}{
				"name":      "echo",
				"arguments": map[string]string{"n": fmt.Sprintf("%d", i)},
			},
		})
	}

	// Read all 10 responses
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	responseIDs := make(map[string]bool)
	for i := 0; i < 10; i++ {
		resp := readJSON(t, reader)
		responseIDs[string(resp["id"])] = true
	}

	// Verify all 10 responses arrived with correct IDs
	for i := 2; i <= 11; i++ {
		assert.True(t, responseIDs[fmt.Sprintf("%d", i)], "should receive response for id %d", i)
	}

	cancel()
}
