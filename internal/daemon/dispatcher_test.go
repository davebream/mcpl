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
		pendingFanout:  make(map[uint64]*rootsAggregator),
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

	server := NewManagedServer("srv", nil, nil)
	go d.dispatchResponse(msg, server)

	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `"id":42`)
	assert.Contains(t, scanner.Text(), `"tools":[]`)
}

func TestDispatchBroadcast(t *testing.T) {
	d, _, scanner := testDaemonWithSession(t, "sess-1", "test-server")

	server := NewManagedServer("test-server", nil, nil)

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

	msg, err := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":99,"method":"ping"}`))
	require.NoError(t, err)

	go d.respondToPing(msg, server)

	scanner := bufio.NewScanner(serverConn)
	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `"id":99`)
	assert.Contains(t, scanner.Text(), `"result":{}`)
}

func TestRootsAggregatorCollectAndFinalize(t *testing.T) {
	serverConn, stdinWriter := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		stdinWriter.Close()
	})

	server := NewManagedServer("test-server", nil, nil)
	server.stdin = stdinWriter

	agg := &rootsAggregator{
		serverID:  json.RawMessage(`5`),
		server:    server,
		remaining: 2,
		roots:     make([]json.RawMessage, 0),
	}

	// First session responds
	done := agg.collect(json.RawMessage(`{"roots":[{"uri":"file:///a","name":"a"}]}`))
	assert.False(t, done)

	// Second session responds with overlapping root
	done = agg.collect(json.RawMessage(`{"roots":[{"uri":"file:///a","name":"a"},{"uri":"file:///b","name":"b"}]}`))
	assert.True(t, done)

	go agg.finalize()

	scanner := bufio.NewScanner(serverConn)
	require.True(t, scanner.Scan())
	text := scanner.Text()

	// Response should have deduplicated roots
	assert.Contains(t, text, `"id":5`)
	assert.Contains(t, text, `file:///a`)
	assert.Contains(t, text, `file:///b`)

	// Verify deduplication: file:///a should appear only once
	var resp struct {
		Result struct {
			Roots []struct {
				URI string `json:"uri"`
			} `json:"roots"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	assert.Len(t, resp.Result.Roots, 2)
}

func TestRootsAggregatorTimeout(t *testing.T) {
	serverConn, stdinWriter := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		stdinWriter.Close()
	})

	server := NewManagedServer("test-server", nil, nil)
	server.stdin = stdinWriter

	agg := &rootsAggregator{
		serverID:  json.RawMessage(`7`),
		server:    server,
		remaining: 2,
		roots:     make([]json.RawMessage, 0),
	}

	// Only one session responds
	done := agg.collect(json.RawMessage(`{"roots":[{"uri":"file:///partial","name":"partial"}]}`))
	assert.False(t, done)

	// Simulate timeout by calling finalize directly
	go agg.finalize()

	scanner := bufio.NewScanner(serverConn)
	require.True(t, scanner.Scan())
	text := scanner.Text()
	assert.Contains(t, text, `"id":7`)
	assert.Contains(t, text, `file:///partial`)
}

func TestRootsAggregatorDuplicateFinalize(t *testing.T) {
	serverConn, stdinWriter := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		stdinWriter.Close()
	})

	server := NewManagedServer("test-server", nil, nil)
	server.stdin = stdinWriter

	agg := &rootsAggregator{
		serverID:  json.RawMessage(`9`),
		server:    server,
		remaining: 1,
		roots:     make([]json.RawMessage, 0),
	}

	agg.collect(json.RawMessage(`{"roots":[{"uri":"file:///x","name":"x"}]}`))

	// First finalize should succeed
	go agg.finalize()

	scanner := bufio.NewScanner(serverConn)
	require.True(t, scanner.Scan())
	assert.Contains(t, scanner.Text(), `file:///x`)

	// Second finalize should be a no-op (done flag prevents duplicate write)
	agg.finalize()
	// No assertion needed — if it wrote again it would hang the test or we'd see a second line
}
