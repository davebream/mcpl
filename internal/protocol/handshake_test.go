package protocol

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectRequest(t *testing.T) {
	t.Run("marshal", func(t *testing.T) {
		req := &ConnectRequest{
			MCPL:   1,
			Type:   "connect",
			Server: "sequential-thinking",
		}
		data, err := json.Marshal(req)
		require.NoError(t, err)
		assert.JSONEq(t, `{"mcpl":1,"type":"connect","server":"sequential-thinking"}`, string(data))
	})

	t.Run("unmarshal", func(t *testing.T) {
		var req ConnectRequest
		err := json.Unmarshal([]byte(`{"mcpl":1,"type":"connect","server":"context7"}`), &req)
		require.NoError(t, err)
		assert.Equal(t, 1, req.MCPL)
		assert.Equal(t, "connect", req.Type)
		assert.Equal(t, "context7", req.Server)
	})
}

func TestConnectResponse(t *testing.T) {
	t.Run("ready response", func(t *testing.T) {
		resp := NewConnectedResponse("ready")
		data, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.JSONEq(t, `{"mcpl":1,"type":"connected","status":"ready"}`, string(data))
	})

	t.Run("starting response", func(t *testing.T) {
		resp := NewConnectedResponse("starting")
		data, err := json.Marshal(resp)
		require.NoError(t, err)
		assert.JSONEq(t, `{"mcpl":1,"type":"connected","status":"starting"}`, string(data))
	})

	t.Run("error response", func(t *testing.T) {
		resp := NewErrorResponse("unknown_server", "server 'foo' not found in config")
		data, err := json.Marshal(resp)
		require.NoError(t, err)

		var parsed map[string]interface{}
		json.Unmarshal(data, &parsed)
		assert.Equal(t, "error", parsed["type"])
		assert.Equal(t, "unknown_server", parsed["code"])
	})
}

func TestValidateHandshake(t *testing.T) {
	t.Run("valid request", func(t *testing.T) {
		req := &ConnectRequest{MCPL: 1, Type: "connect", Server: "test"}
		err := ValidateHandshake(req, 1)
		assert.NoError(t, err)
	})

	t.Run("version mismatch", func(t *testing.T) {
		req := &ConnectRequest{MCPL: 2, Type: "connect", Server: "test"}
		err := ValidateHandshake(req, 1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "version")
	})

	t.Run("wrong type", func(t *testing.T) {
		req := &ConnectRequest{MCPL: 1, Type: "invalid", Server: "test"}
		err := ValidateHandshake(req, 1)
		assert.Error(t, err)
	})

	t.Run("empty server name", func(t *testing.T) {
		req := &ConnectRequest{MCPL: 1, Type: "connect", Server: ""}
		err := ValidateHandshake(req, 1)
		assert.Error(t, err)
	})
}
