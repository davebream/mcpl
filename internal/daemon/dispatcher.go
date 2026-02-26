package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/davebream/mcpl/internal/protocol"
)

// rootsAggregator collects roots/list responses from multiple sessions and
// merges them into a single response sent back to the server.
type rootsAggregator struct {
	serverID  json.RawMessage // Server's original request ID
	server    *ManagedServer
	mu        sync.Mutex
	remaining int32
	roots     []json.RawMessage // individual root objects collected
	done      bool
}

// collect adds roots from one session's response. Returns true if all sessions responded.
func (a *rootsAggregator) collect(result json.RawMessage) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.done {
		return false
	}

	var parsed struct {
		Roots []json.RawMessage `json:"roots"`
	}
	if err := json.Unmarshal(result, &parsed); err == nil {
		a.roots = append(a.roots, parsed.Roots...)
	}

	a.remaining--
	return a.remaining <= 0
}

// finalize sends the merged response to the server (once).
func (a *rootsAggregator) finalize() {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	a.done = true
	a.mu.Unlock()

	// Deduplicate roots by URI
	seen := make(map[string]bool)
	unique := make([]json.RawMessage, 0, len(a.roots))
	for _, root := range a.roots {
		var r struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(root, &r) == nil && !seen[r.URI] {
			seen[r.URI] = true
			unique = append(unique, root)
		}
	}

	rootsArray, _ := json.Marshal(unique)
	result := json.RawMessage(fmt.Sprintf(`{"roots":%s}`, rootsArray))

	resp := &protocol.Message{
		JSONRPC: "2.0",
		ID:      a.serverID,
		Result:  result,
	}
	data, _ := resp.Serialize()
	a.server.WriteToStdin(data)
}

// startServerReader starts a goroutine that reads from server stdout
// and dispatches messages to the correct shim sessions.
// Called once per server, on first shim connection.
func (d *Daemon) startServerReader(server *ManagedServer) {
	scanner := bufio.NewScanner(server.stdout)
	scanner.Buffer(make([]byte, 0, 4096), 10*1024*1024) // 10MB max

	go func() {
		for scanner.Scan() {
			line := scanner.Bytes()
			lineCopy := make([]byte, len(line))
			copy(lineCopy, line)

			msg, err := protocol.ParseMessage(lineCopy)
			if err != nil {
				d.logger.Warn("invalid message from server", "server", server.name, "error", err)
				continue
			}

			d.dispatchServerMessage(msg, server)
		}
		d.logger.Info("server reader exited", "server", server.name)
	}()
}

func (d *Daemon) dispatchServerMessage(msg *protocol.Message, server *ManagedServer) {
	class := protocol.ClassifyServerMessage(msg)

	switch class {
	case protocol.ClassResponse:
		d.dispatchResponse(msg, server)

	case protocol.ClassProgress:
		d.dispatchProgress(msg)

	case protocol.ClassResourceUpdated:
		d.dispatchResourceUpdate(msg, server)

	case protocol.ClassBroadcast:
		d.broadcastToSessions(msg, server)

	case protocol.ClassPing:
		d.respondToPing(msg, server)

	case protocol.ClassServerRequest:
		d.handleServerRequest(msg, server)
	}
}

func (d *Daemon) dispatchResponse(msg *protocol.Message, server *ManagedServer) {
	// Check if this is an initialize response before remapping ID
	var rawGlobalID uint64
	if err := json.Unmarshal(msg.ID, &rawGlobalID); err == nil {
		d.mu.Lock()
		serverName, isInitResp := d.pendingInit[rawGlobalID]
		if isInitResp {
			delete(d.pendingInit, rawGlobalID)
		}
		d.mu.Unlock()

		if isInitResp && msg.Result != nil {
			d.initCache.Store(serverName, msg.Result)
			d.logger.Info("cached initialize response", "server", serverName)
		}
	}

	sessionID, err := rewriteResponseID(msg, d.idMapper)
	if err != nil {
		d.logger.Warn("response routing failed", "error", err)
		// Still signal the waiter so the queue doesn't block
		server.SignalSerializeWaiter(rawGlobalID)
		return
	}

	d.mu.Lock()
	session, ok := d.sessions[sessionID]
	d.mu.Unlock()

	if ok {
		data, _ := msg.Serialize()
		session.WriteLine(data)
	}

	// Signal serialize waiter — unblocks processLoop to handle next request
	server.SignalSerializeWaiter(rawGlobalID)
}

func (d *Daemon) dispatchProgress(msg *protocol.Message) {
	// Extract progressToken from params
	var params struct {
		ProgressToken json.RawMessage `json:"progressToken"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}

	var token string
	if err := json.Unmarshal(params.ProgressToken, &token); err != nil {
		// Try integer
		var intToken int64
		if err := json.Unmarshal(params.ProgressToken, &intToken); err != nil {
			return
		}
		token = fmt.Sprintf("%d", intToken)
	}

	d.mu.Lock()
	sessionID, ok := d.progressTokens[token]
	session := d.sessions[sessionID]
	d.mu.Unlock()

	if ok && session != nil {
		data, _ := msg.Serialize()
		session.WriteLine(data)
	}
}

func (d *Daemon) dispatchResourceUpdate(msg *protocol.Message, server *ManagedServer) {
	uri, ok := protocol.ExtractResourceURI(msg)
	if !ok {
		return
	}

	subscribers := d.subs.Subscribers(uri)
	data, _ := msg.Serialize()

	// Collect sessions under lock, write outside to avoid holding mutex during I/O
	d.mu.Lock()
	sessions := make([]*Session, 0, len(subscribers))
	for _, sessionID := range subscribers {
		if session, ok := d.sessions[sessionID]; ok {
			sessions = append(sessions, session)
		}
	}
	d.mu.Unlock()

	for _, session := range sessions {
		session.WriteLine(data)
	}
}

// sessionsForServer returns all sessions connected to the given server.
// Caller must NOT hold d.mu.
func (d *Daemon) sessionsForServer(serverName string) []*Session {
	d.mu.Lock()
	defer d.mu.Unlock()
	var sessions []*Session
	for _, session := range d.sessions {
		if session.ServerName == serverName {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

func (d *Daemon) broadcastToSessions(msg *protocol.Message, server *ManagedServer) {
	data, _ := msg.Serialize()
	for _, session := range d.sessionsForServer(server.name) {
		session.WriteLine(data)
	}
}

func (d *Daemon) respondToPing(msg *protocol.Message, server *ManagedServer) {
	// Daemon responds directly — shims don't see keepalives
	pong := &protocol.Message{
		JSONRPC: "2.0",
		ID:      msg.ID,
		Result:  json.RawMessage(`{}`),
	}
	data, _ := pong.Serialize()
	server.WriteToStdin(data)
}

func (d *Daemon) handleServerRequest(msg *protocol.Message, server *ManagedServer) {
	switch msg.Method {
	case "roots/list":
		d.handleRootsList(msg, server)
	case "sampling/createMessage":
		d.handleSampling(msg, server)
	default:
		d.logger.Warn("unknown server request", "method", msg.Method)
		errResp := &protocol.Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error:   json.RawMessage(`{"code":-32601,"message":"method not found"}`),
		}
		data, _ := errResp.Serialize()
		server.WriteToStdin(data)
	}
}

// handleRootsList fans out roots/list to all sessions that support roots,
// merges and deduplicates the results by URI, and responds to the server.
func (d *Daemon) handleRootsList(msg *protocol.Message, server *ManagedServer) {
	sessions := d.sessionsForServer(server.name)

	// Filter to sessions that support roots
	var capable []*Session
	for _, s := range sessions {
		if s.Capabilities.Roots {
			capable = append(capable, s)
		}
	}

	if len(capable) == 0 {
		resp := &protocol.Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  json.RawMessage(`{"roots":[]}`),
		}
		data, _ := resp.Serialize()
		server.WriteToStdin(data)
		return
	}

	agg := &rootsAggregator{
		serverID:  msg.ID,
		server:    server,
		remaining: int32(len(capable)),
		roots:     make([]json.RawMessage, 0),
	}

	d.mu.Lock()
	for _, s := range capable {
		fanoutID := d.idMapper.NextID()
		perSessionMsg := &protocol.Message{
			JSONRPC: "2.0",
			ID:      json.RawMessage(fmt.Sprintf("%d", fanoutID)),
			Method:  "roots/list",
		}
		d.pendingFanout[fanoutID] = agg
		data, _ := perSessionMsg.Serialize()
		s.WriteLine(data)
	}
	d.mu.Unlock()

	// Timeout — finalize after 5s if not all sessions responded
	go func() {
		time.Sleep(5 * time.Second)
		agg.finalize()
	}()
}

// handleSampling routes sampling/createMessage to one session that supports sampling.
func (d *Daemon) handleSampling(msg *protocol.Message, server *ManagedServer) {
	sessions := d.sessionsForServer(server.name)

	// Find a session that supports sampling
	var target *Session
	for _, s := range sessions {
		if s.Capabilities.Sampling {
			target = s
			break
		}
	}

	if target == nil {
		errResp := &protocol.Message{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Error:   json.RawMessage(`{"code":-32601,"message":"no connected client supports sampling"}`),
		}
		data, _ := errResp.Serialize()
		server.WriteToStdin(data)
		return
	}

	// Forward directly — server's ID preserved, response flows back via sessionLoop
	data, _ := msg.Serialize()
	target.WriteLine(data)
}

