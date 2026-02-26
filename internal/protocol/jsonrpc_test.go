package protocol

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMessage(t *testing.T) {
	t.Run("request with integer id", func(t *testing.T) {
		msg, err := ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		require.NoError(t, err)
		assert.True(t, msg.IsRequest())
		assert.False(t, msg.IsResponse())
		assert.False(t, msg.IsNotification())
		assert.Equal(t, "tools/list", msg.Method)
	})

	t.Run("request with string id", func(t *testing.T) {
		msg, err := ParseMessage([]byte(`{"jsonrpc":"2.0","id":"abc","method":"tools/call"}`))
		require.NoError(t, err)
		assert.True(t, msg.IsRequest())
		assert.Equal(t, json.RawMessage(`"abc"`), msg.ID)
	})

	t.Run("response with result", func(t *testing.T) {
		msg, err := ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))
		require.NoError(t, err)
		assert.True(t, msg.IsResponse())
		assert.False(t, msg.IsRequest())
	})

	t.Run("response with error", func(t *testing.T) {
		msg, err := ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"bad"}}`))
		require.NoError(t, err)
		assert.True(t, msg.IsResponse())
	})

	t.Run("notification (no id)", func(t *testing.T) {
		msg, err := ParseMessage([]byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`))
		require.NoError(t, err)
		assert.True(t, msg.IsNotification())
		assert.False(t, msg.IsRequest())
		assert.False(t, msg.IsResponse())
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := ParseMessage([]byte(`{invalid`))
		assert.Error(t, err)
	})
}

func TestMessageSetID(t *testing.T) {
	msg, _ := ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))

	msg.SetID(json.RawMessage(`42`))
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"id":42`)
}

func TestMessageSerialize(t *testing.T) {
	msg, _ := ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	data, err := msg.Serialize()
	require.NoError(t, err)
	assert.Contains(t, string(data), `"jsonrpc":"2.0"`)
	assert.Contains(t, string(data), `"method":"tools/list"`)
}

func TestIDMapper(t *testing.T) {
	t.Run("map and unmap", func(t *testing.T) {
		m := NewIDMapper()

		globalID := m.Map(json.RawMessage(`1`), "session-a")
		assert.Equal(t, uint64(1), globalID)

		mapping, ok := m.Unmap(globalID)
		require.True(t, ok)
		assert.Equal(t, json.RawMessage(`1`), mapping.OriginalID)
		assert.Equal(t, "session-a", mapping.SessionID)
	})

	t.Run("monotonic counter", func(t *testing.T) {
		m := NewIDMapper()
		id1 := m.Map(json.RawMessage(`1`), "s1")
		id2 := m.Map(json.RawMessage(`1`), "s2")
		id3 := m.Map(json.RawMessage(`2`), "s1")

		assert.Equal(t, uint64(1), id1)
		assert.Equal(t, uint64(2), id2)
		assert.Equal(t, uint64(3), id3)
	})

	t.Run("unmap nonexistent returns false", func(t *testing.T) {
		m := NewIDMapper()
		_, ok := m.Unmap(999)
		assert.False(t, ok)
	})

	t.Run("NextID generates unique IDs without mapping", func(t *testing.T) {
		m := NewIDMapper()
		id1 := m.NextID()
		id2 := m.NextID()
		assert.NotEqual(t, id1, id2)
		// NextID IDs should not be unmappable
		_, ok := m.Unmap(id1)
		assert.False(t, ok)
	})
}
