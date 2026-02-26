package protocol

import (
	"encoding/json"
	"fmt"
	"sync"
)

// MessageClass categorizes server-originated messages for routing.
type MessageClass int

const (
	ClassResponse        MessageClass = iota // Response to a shim's request
	ClassProgress                            // notifications/progress -> route by progressToken
	ClassResourceUpdated                     // notifications/resources/updated -> route by subscription
	ClassBroadcast                           // tools/list_changed, resources/list_changed, logging/message -> all shims
	ClassPing                                // ping -> daemon responds directly
	ClassServerRequest                       // roots/list, sampling/createMessage -> special handling
)

func ClassifyServerMessage(msg *Message) MessageClass {
	if msg.IsResponse() {
		return ClassResponse
	}
	switch msg.Method {
	case "notifications/progress":
		return ClassProgress
	case "notifications/resources/updated":
		return ClassResourceUpdated
	case "notifications/tools/list_changed",
		"notifications/resources/list_changed",
		"logging/message":
		return ClassBroadcast
	case "ping":
		return ClassPing
	case "roots/list", "sampling/createMessage":
		return ClassServerRequest
	default:
		return ClassBroadcast
	}
}

// InitCache stores cached initialize responses per server.
type InitCache struct {
	mu     sync.RWMutex
	cached map[string]json.RawMessage
}

func NewInitCache() *InitCache {
	return &InitCache{cached: make(map[string]json.RawMessage)}
}

func (c *InitCache) Store(serverName string, result json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make(json.RawMessage, len(result))
	copy(cp, result)
	c.cached[serverName] = cp
}

func (c *InitCache) Get(serverName string) (json.RawMessage, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result, ok := c.cached[serverName]
	return result, ok
}

// SubscriptionTracker maintains URI -> Set<sessionID> for resource subscriptions.
type SubscriptionTracker struct {
	mu            sync.Mutex
	subscriptions map[string]map[string]bool // URI -> set of session IDs
}

func NewSubscriptionTracker() *SubscriptionTracker {
	return &SubscriptionTracker{subscriptions: make(map[string]map[string]bool)}
}

// Subscribe adds a session to a URI's subscriber set.
// Returns the subscriber count after adding (1 = first subscriber).
func (s *SubscriptionTracker) Subscribe(uri, sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscriptions[uri] == nil {
		s.subscriptions[uri] = make(map[string]bool)
	}
	s.subscriptions[uri][sessionID] = true
	return len(s.subscriptions[uri])
}

// Unsubscribe removes a session from a URI's subscriber set.
// Returns the subscriber count after removing (0 = last subscriber gone).
func (s *SubscriptionTracker) Unsubscribe(uri, sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessions, ok := s.subscriptions[uri]; ok {
		delete(sessions, sessionID)
		if len(sessions) == 0 {
			delete(s.subscriptions, uri)
			return 0
		}
		return len(sessions)
	}
	return 0
}

// Subscribers returns all session IDs subscribed to a URI.
func (s *SubscriptionTracker) Subscribers(uri string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions, ok := s.subscriptions[uri]
	if !ok {
		return nil
	}
	result := make([]string, 0, len(sessions))
	for id := range sessions {
		result = append(result, id)
	}
	return result
}

// RemoveSession removes a session from all subscriptions.
// Returns URIs where this was the last subscriber (need unsubscribe from server).
func (s *SubscriptionTracker) RemoveSession(sessionID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var orphaned []string
	for uri, sessions := range s.subscriptions {
		if sessions[sessionID] {
			delete(sessions, sessionID)
			if len(sessions) == 0 {
				delete(s.subscriptions, uri)
				orphaned = append(orphaned, uri)
			}
		}
	}
	return orphaned
}

// ExtractProgressToken extracts the progressToken from a request's _meta params.
func ExtractProgressToken(msg *Message) (string, bool) {
	if len(msg.Params) == 0 {
		return "", false
	}
	var params struct {
		Meta *struct {
			ProgressToken json.RawMessage `json:"progressToken"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil || params.Meta == nil || len(params.Meta.ProgressToken) == 0 {
		return "", false
	}
	// Handle both string and integer tokens
	var strToken string
	if err := json.Unmarshal(params.Meta.ProgressToken, &strToken); err == nil {
		return strToken, true
	}
	var intToken int64
	if err := json.Unmarshal(params.Meta.ProgressToken, &intToken); err == nil {
		return fmt.Sprintf("%d", intToken), true
	}
	return "", false
}

// ExtractResourceURI extracts the URI from a resource notification's params.
func ExtractResourceURI(msg *Message) (string, bool) {
	if len(msg.Params) == 0 {
		return "", false
	}
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil || params.URI == "" {
		return "", false
	}
	return params.URI, true
}
