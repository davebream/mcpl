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

func TestRewriteInitCapabilities(t *testing.T) {
	msg, _ := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`))

	rewriteInitCapabilities(msg)

	// Verify maximal capabilities are set
	var params map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(msg.Params, &params))

	var caps struct {
		Roots    *json.RawMessage `json:"roots"`
		Sampling *json.RawMessage `json:"sampling"`
	}
	require.NoError(t, json.Unmarshal(params["capabilities"], &caps))
	assert.NotNil(t, caps.Roots, "roots capability should be set")
	assert.NotNil(t, caps.Sampling, "sampling capability should be set")

	// Verify other fields preserved
	assert.Contains(t, string(params["protocolVersion"]), "2025-03-26")
	assert.Contains(t, string(params["clientInfo"]), "test")
}

func TestRewriteInitCapabilitiesPreservesExistingFields(t *testing.T) {
	msg, _ := protocol.ParseMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{"roots":{"listChanged":false}},"clientInfo":{"name":"claude","version":"2.0"}}}`))

	rewriteInitCapabilities(msg)

	var params map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(msg.Params, &params))

	// protocolVersion and clientInfo should be preserved
	assert.JSONEq(t, `"2025-03-26"`, string(params["protocolVersion"]))
	assert.Contains(t, string(params["clientInfo"]), "claude")

	// capabilities should be maximal
	assert.Contains(t, string(params["capabilities"]), `"roots"`)
	assert.Contains(t, string(params["capabilities"]), `"sampling"`)
}
