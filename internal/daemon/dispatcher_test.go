package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"

	"github.com/davebream/mcpl/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDaemonWithSession creates a Daemon with a connected session backed by a pipe.
// Returns the daemon, session, and a scanner to read what the session receives.
// Dispatches must run in a goroutine since net.Pipe is synchronous.
func testDaemonWithSession(t *testing.T, sessionID, serverName string) (*Daemon, *Session, *bufio.Scanner) {
	t.Helper()

	d := &Daemon{
		sessions:       make(map[string]*Session),
		servers:        make(map[string]*ManagedServer),
		idMapper:       protocol.NewIDMapper(),
		initCache:      protocol.NewInitCache(),
		subs:           protocol.NewSubscriptionTracker(),
		progressTokens: make(map[string]string),
	}

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	session := &Session{
		ID:         sessionID,
		Conn:       serverConn,
		ServerName: serverName,
		scanner:    bufio.NewScanner(serverConn),
	}

	d.sessions[sessionID] = session

	scanner := bufio.NewScanner(clientConn)
	scanner.Buffer(make([]byte, 0, 4096), 10*1024*1024)

	return d, session, scanner
}

func TestDispatchResponse(t *testing.T) {
	d, _, scanner := testDaemonWithSession(t, "sess-1", "srv")

	// Map a request ID so the response can be routed back
	d.idMapper.Map(json.RawMessage(`42`), "sess-1") // globalID = 1

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
	require.NoError(t, err)

	go d.dispatchResponse(msg)

	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `"id":42`)
	assert.Contains(t, scanner.Text(), `"tools":[]`)
}

func TestDispatchBroadcast(t *testing.T) {
	d, _, scanner := testDaemonWithSession(t, "sess-1", "test-server")

	server := NewManagedServer("test-server", nil)

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`))
	require.NoError(t, err)

	go d.broadcastToSessions(msg, server)

	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `notifications/tools/list_changed`)
}

func TestDispatchProgress(t *testing.T) {
	d, _, scanner := testDaemonWithSession(t, "sess-1", "srv")

	d.progressTokens["my-token"] = "sess-1"

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"my-token","progress":50}}`))
	require.NoError(t, err)

	go d.dispatchProgress(msg)

	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `my-token`)
	assert.Contains(t, scanner.Text(), `"progress":50`)
}

func TestDispatchPing(t *testing.T) {
	serverConn, stdinWriter := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		stdinWriter.Close()
	})

	server := NewManagedServer("test-server", nil)
	server.stdin = stdinWriter

	d := &Daemon{
		sessions:       make(map[string]*Session),
		servers:        make(map[string]*ManagedServer),
		idMapper:       protocol.NewIDMapper(),
		initCache:      protocol.NewInitCache(),
		subs:           protocol.NewSubscriptionTracker(),
		progressTokens: make(map[string]string),
	}

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":99,"method":"ping"}`))
	require.NoError(t, err)

	go d.respondToPing(msg, server)

	scanner := bufio.NewScanner(serverConn)
	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `"id":99`)
	assert.Contains(t, scanner.Text(), `"result":{}`)
}
