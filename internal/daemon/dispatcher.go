package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"

	"github.com/davebream/mcpl/internal/protocol"
)

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
		d.dispatchResponse(msg)

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

func (d *Daemon) dispatchResponse(msg *protocol.Message) {
	sessionID, err := rewriteResponseID(msg, d.idMapper)
	if err != nil {
		d.logger.Warn("response routing failed", "error", err)
		return
	}

	// Cache initialize response
	if msg.Result != nil {
		// Check if this was an initialize response by looking up the original method
		// (stored during Map). For now, cache based on init state.
	}

	d.mu.Lock()
	session, ok := d.sessions[sessionID]
	d.mu.Unlock()

	if ok {
		data, _ := msg.Serialize()
		session.WriteLine(data)
	}
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

	d.mu.Lock()
	for _, sessionID := range subscribers {
		if session, ok := d.sessions[sessionID]; ok {
			session.WriteLine(data)
		}
	}
	d.mu.Unlock()
}

func (d *Daemon) broadcastToSessions(msg *protocol.Message, server *ManagedServer) {
	data, _ := msg.Serialize()

	d.mu.Lock()
	for _, session := range d.sessions {
		if session.ServerName == server.name {
			session.WriteLine(data)
		}
	}
	d.mu.Unlock()
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
	// Implemented in Task 10d
}

