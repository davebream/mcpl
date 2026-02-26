package protocol

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitCache(t *testing.T) {
	t.Run("store and retrieve", func(t *testing.T) {
		cache := NewInitCache()
		result := json.RawMessage(`{"capabilities":{"tools":{}}}`)

		cache.Store("context7", result)

		got, ok := cache.Get("context7")
		assert.True(t, ok)
		assert.Equal(t, result, got)
	})

	t.Run("miss returns false", func(t *testing.T) {
		cache := NewInitCache()
		_, ok := cache.Get("nonexistent")
		assert.False(t, ok)
	})

	t.Run("invalidate removes entry", func(t *testing.T) {
		cache := NewInitCache()
		cache.Store("test", json.RawMessage(`{}`))
		cache.Invalidate("test")
		_, ok := cache.Get("test")
		assert.False(t, ok)
	})
}

func TestClassifyServerMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected MessageClass
	}{
		{
			name:     "response with id",
			input:    `{"jsonrpc":"2.0","id":7,"result":{}}`,
			expected: ClassResponse,
		},
		{
			name:     "progress notification",
			input:    `{"jsonrpc":"2.0","method":"notifications/progress","params":{"progressToken":"tok1","progress":50}}`,
			expected: ClassProgress,
		},
		{
			name:     "resource updated notification",
			input:    `{"jsonrpc":"2.0","method":"notifications/resources/updated","params":{"uri":"file:///foo"}}`,
			expected: ClassResourceUpdated,
		},
		{
			name:     "tools list changed",
			input:    `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`,
			expected: ClassBroadcast,
		},
		{
			name:     "resources list changed",
			input:    `{"jsonrpc":"2.0","method":"notifications/resources/list_changed"}`,
			expected: ClassBroadcast,
		},
		{
			name:     "logging message",
			input:    `{"jsonrpc":"2.0","method":"logging/message","params":{"level":"info","data":"hello"}}`,
			expected: ClassBroadcast,
		},
		{
			name:     "ping from server",
			input:    `{"jsonrpc":"2.0","id":1,"method":"ping"}`,
			expected: ClassPing,
		},
		{
			name:     "roots/list request from server",
			input:    `{"jsonrpc":"2.0","id":5,"method":"roots/list"}`,
			expected: ClassServerRequest,
		},
		{
			name:     "sampling request from server",
			input:    `{"jsonrpc":"2.0","id":6,"method":"sampling/createMessage","params":{}}`,
			expected: ClassServerRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ParseMessage([]byte(tt.input))
			require.NoError(t, err)
			assert.Equal(t, tt.expected, ClassifyServerMessage(msg))
		})
	}
}

func TestSubscriptionTracker(t *testing.T) {
	t.Run("subscribe and lookup", func(t *testing.T) {
		tracker := NewSubscriptionTracker()
		tracker.Subscribe("file:///foo", "session-a")
		tracker.Subscribe("file:///foo", "session-b")

		sessions := tracker.Subscribers("file:///foo")
		assert.ElementsMatch(t, []string{"session-a", "session-b"}, sessions)
	})

	t.Run("unsubscribe", func(t *testing.T) {
		tracker := NewSubscriptionTracker()
		tracker.Subscribe("file:///foo", "session-a")
		tracker.Subscribe("file:///foo", "session-b")
		tracker.Unsubscribe("file:///foo", "session-a")

		sessions := tracker.Subscribers("file:///foo")
		assert.Equal(t, []string{"session-b"}, sessions)
	})

	t.Run("remove session returns orphaned URIs", func(t *testing.T) {
		tracker := NewSubscriptionTracker()
		tracker.Subscribe("file:///foo", "session-a")
		tracker.Subscribe("file:///bar", "session-a")
		tracker.Subscribe("file:///bar", "session-b")

		orphaned := tracker.RemoveSession("session-a")
		// file:///foo had only session-a -> orphaned
		// file:///bar still has session-b -> not orphaned
		assert.Equal(t, []string{"file:///foo"}, orphaned)
	})

	t.Run("no subscribers returns empty", func(t *testing.T) {
		tracker := NewSubscriptionTracker()
		assert.Empty(t, tracker.Subscribers("nonexistent"))
	})
}

func TestExtractProgressToken(t *testing.T) {
	t.Run("from request params", func(t *testing.T) {
		msg, _ := ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"progressToken":"tok-123"},"name":"test"}}`))
		token, ok := ExtractProgressToken(msg)
		assert.True(t, ok)
		assert.Equal(t, "tok-123", token)
	})

	t.Run("no token", func(t *testing.T) {
		msg, _ := ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"test"}}`))
		_, ok := ExtractProgressToken(msg)
		assert.False(t, ok)
	})

	t.Run("integer progress token", func(t *testing.T) {
		msg, _ := ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"_meta":{"progressToken":42}}}`))
		token, ok := ExtractProgressToken(msg)
		assert.True(t, ok)
		assert.Equal(t, "42", token)
	})
}

func TestExtractResourceURI(t *testing.T) {
	msg, _ := ParseMessage([]byte(`{"jsonrpc":"2.0","method":"notifications/resources/updated","params":{"uri":"file:///test.txt"}}`))
	uri, ok := ExtractResourceURI(msg)
	assert.True(t, ok)
	assert.Equal(t, "file:///test.txt", uri)
}
