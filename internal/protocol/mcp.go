package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
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
	c.cached[serverName] = result
}

func (c *InitCache) Get(serverName string) (json.RawMessage, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result, ok := c.cached[serverName]
	return result, ok
}

func (c *InitCache) Invalidate(serverName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cached, serverName)
}

// SubscriptionTracker maintains URI -> Set<sessionID> for resource subscriptions.
type SubscriptionTracker struct {
	mu            sync.Mutex
	subscriptions map[string]map[string]bool // URI -> set of session IDs
}

func NewSubscriptionTracker() *SubscriptionTracker {
	return &SubscriptionTracker{subscriptions: make(map[string]map[string]bool)}
}

func (s *SubscriptionTracker) Subscribe(uri, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscriptions[uri] == nil {
		s.subscriptions[uri] = make(map[string]bool)
	}
	s.subscriptions[uri][sessionID] = true
}

func (s *SubscriptionTracker) Unsubscribe(uri, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessions, ok := s.subscriptions[uri]; ok {
		delete(sessions, sessionID)
		if len(sessions) == 0 {
			delete(s.subscriptions, uri)
		}
	}
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
	sort.Strings(result)
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
	sort.Strings(orphaned)
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
