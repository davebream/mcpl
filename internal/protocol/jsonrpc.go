package protocol

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Message represents a JSON-RPC 2.0 message (request, response, or notification).
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func ParseMessage(data []byte) (*Message, error) {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("parse JSON-RPC message: %w", err)
	}
	return &msg, nil
}

// IsRequest returns true if message has both id and method (a request).
func (m *Message) IsRequest() bool {
	return len(m.ID) > 0 && m.Method != ""
}

// IsResponse returns true if message has id but no method (a response).
func (m *Message) IsResponse() bool {
	return len(m.ID) > 0 && m.Method == ""
}

// IsNotification returns true if message has method but no id.
func (m *Message) IsNotification() bool {
	return len(m.ID) == 0 && m.Method != ""
}

func (m *Message) SetID(id json.RawMessage) {
	m.ID = id
}

func (m *Message) Serialize() ([]byte, error) {
	return json.Marshal(m)
}

// IDMapping records the association between a global ID and the original shim request.
type IDMapping struct {
	GlobalID   uint64
	OriginalID json.RawMessage
	SessionID  string
	CreatedAt  time.Time
}

// IDMapper provides globally unique ID generation and bidirectional mapping.
type IDMapper struct {
	mu       sync.Mutex
	counter  atomic.Uint64
	mappings map[uint64]*IDMapping
}

func NewIDMapper() *IDMapper {
	return &IDMapper{
		mappings: make(map[uint64]*IDMapping),
	}
}

// Map assigns a globally unique ID and records the mapping.
// Note: concurrent calls produce unique IDs but map insertions are not strictly ordered by ID.
func (m *IDMapper) Map(originalID json.RawMessage, sessionID string) uint64 {
	globalID := m.counter.Add(1)
	m.mu.Lock()
	m.mappings[globalID] = &IDMapping{
		GlobalID:   globalID,
		OriginalID: originalID,
		SessionID:  sessionID,
		CreatedAt:  time.Now(),
	}
	m.mu.Unlock()
	return globalID
}

// Unmap retrieves and removes the mapping for a global ID.
func (m *IDMapper) Unmap(globalID uint64) (*IDMapping, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mapping, ok := m.mappings[globalID]
	if ok {
		delete(m.mappings, globalID)
	}
	return mapping, ok
}

// GC removes mappings older than maxAge.
func (m *IDMapper) GC(maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, mapping := range m.mappings {
		if mapping.CreatedAt.Before(cutoff) {
			delete(m.mappings, id)
		}
	}
}
