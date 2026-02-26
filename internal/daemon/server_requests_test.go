package daemon

import (
	"bufio"
	"net"
	"testing"

	"github.com/davebream/mcpl/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsRootsListRequest(t *testing.T) {
	msg, _ := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":5,"method":"roots/list"}`))
	assert.Equal(t, protocol.ClassServerRequest, protocol.ClassifyServerMessage(msg))
	assert.Equal(t, "roots/list", msg.Method)
}

func TestIsSamplingRequest(t *testing.T) {
	msg, _ := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":6,"method":"sampling/createMessage","params":{}}`))
	assert.Equal(t, protocol.ClassServerRequest, protocol.ClassifyServerMessage(msg))
	assert.Equal(t, "sampling/createMessage", msg.Method)
}

func TestHandleRootsListNoSessions(t *testing.T) {
	// Server stdin pipe to capture the empty-roots response
	serverConn, stdinWriter := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		stdinWriter.Close()
	})

	server := NewManagedServer("test-server", nil, nil)
	server.stdin = stdinWriter

	d := &Daemon{
		sessions:       make(map[string]*Session),
		servers:        make(map[string]*ManagedServer),
		idMapper:       protocol.NewIDMapper(),
		initCache:      protocol.NewInitCache(),
		subs:           protocol.NewSubscriptionTracker(),
		progressTokens: make(map[string]string),
		pendingFanout:  make(map[uint64]*rootsAggregator),
	}

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":5,"method":"roots/list"}`))
	require.NoError(t, err)

	go d.handleRootsList(msg, server)

	scanner := bufio.NewScanner(serverConn)
	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `"roots":[]`)
	assert.Contains(t, scanner.Text(), `"id":5`)
}

func TestHandleRootsListFansOutToCapableSession(t *testing.T) {
	d, session, sessionScanner := testDaemonWithSession(t, "sess-1", "test-server")
	session.Capabilities.Roots = true

	serverConn, stdinWriter := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		stdinWriter.Close()
	})
	server := NewManagedServer("test-server", nil, nil)
	server.stdin = stdinWriter

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":5,"method":"roots/list"}`))
	require.NoError(t, err)

	go d.handleRootsList(msg, server)

	// Session should receive a fan-out roots/list request with a new ID
	require.True(t, sessionScanner.Scan())
	assert.Contains(t, sessionScanner.Text(), `roots/list`)
	assert.Contains(t, sessionScanner.Text(), `"method"`)
}

func TestHandleRootsListNoCapableSessions(t *testing.T) {
	// Session exists but doesn't support roots
	d, _, _ := testDaemonWithSession(t, "sess-1", "test-server")

	serverConn, stdinWriter := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		stdinWriter.Close()
	})
	server := NewManagedServer("test-server", nil, nil)
	server.stdin = stdinWriter

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":5,"method":"roots/list"}`))
	require.NoError(t, err)

	go d.handleRootsList(msg, server)

	scanner := bufio.NewScanner(serverConn)
	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `"roots":[]`)
	assert.Contains(t, scanner.Text(), `"id":5`)
}

func TestHandleSamplingForwardsToCapableSession(t *testing.T) {
	d, session, sessionScanner := testDaemonWithSession(t, "sess-1", "test-server")
	session.Capabilities.Sampling = true

	server := NewManagedServer("test-server", nil, nil)

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":6,"method":"sampling/createMessage","params":{}}`))
	require.NoError(t, err)

	go d.handleSampling(msg, server)

	require.True(t, sessionScanner.Scan())
	assert.Contains(t, sessionScanner.Text(), `sampling/createMessage`)
}

func TestHandleSamplingNoSessions(t *testing.T) {
	serverConn, stdinWriter := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		stdinWriter.Close()
	})

	server := NewManagedServer("test-server", nil, nil)
	server.stdin = stdinWriter

	d := &Daemon{
		sessions:       make(map[string]*Session),
		servers:        make(map[string]*ManagedServer),
		idMapper:       protocol.NewIDMapper(),
		initCache:      protocol.NewInitCache(),
		subs:           protocol.NewSubscriptionTracker(),
		progressTokens: make(map[string]string),
		pendingFanout:  make(map[uint64]*rootsAggregator),
	}

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":6,"method":"sampling/createMessage","params":{}}`))
	require.NoError(t, err)

	go d.handleSampling(msg, server)

	scanner := bufio.NewScanner(serverConn)
	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `"error"`)
	assert.Contains(t, scanner.Text(), `no connected client supports sampling`)
}

func TestHandleSamplingNoCapableSessions(t *testing.T) {
	// Session exists but doesn't support sampling
	d, _, _ := testDaemonWithSession(t, "sess-1", "test-server")

	serverConn, stdinWriter := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		stdinWriter.Close()
	})

	server := NewManagedServer("test-server", nil, nil)
	server.stdin = stdinWriter

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":6,"method":"sampling/createMessage","params":{}}`))
	require.NoError(t, err)

	go d.handleSampling(msg, server)

	scanner := bufio.NewScanner(serverConn)
	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `"error"`)
	assert.Contains(t, scanner.Text(), `no connected client supports sampling`)
}
