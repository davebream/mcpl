package daemon

import (
	"encoding/json"
	"testing"

	"github.com/davebream/mcpl/internal/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteRequestID(t *testing.T) {
	mapper := protocol.NewIDMapper()
	msg, _ := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))

	globalID := rewriteRequestID(msg, mapper, "session-a")

	assert.Equal(t, uint64(1), globalID)

	data, _ := msg.Serialize()
	assert.Contains(t, string(data), `"id":1`) // ID was rewritten to globalID 1
}

func TestRewriteResponseID(t *testing.T) {
	mapper := protocol.NewIDMapper()
	// First map a request
	mapper.Map(json.RawMessage(`42`), "session-a")

	// Now simulate a response with global ID 1
	msg, _ := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`))

	sessionID, err := rewriteResponseID(msg, mapper)
	require.NoError(t, err)
	assert.Equal(t, "session-a", sessionID)

	// ID should be restored to original
	data, _ := msg.Serialize()
	assert.Contains(t, string(data), `"id":42`)
}

func TestIsInitializeRequest(t *testing.T) {
	msg, _ := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	assert.True(t, isInitializeRequest(msg))

	msg2, _ := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	assert.False(t, isInitializeRequest(msg2))
}

func TestIsInitializedNotification(t *testing.T) {
	msg, _ := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","method":"initialized"}`))
	assert.True(t, isInitializedNotification(msg))
}
