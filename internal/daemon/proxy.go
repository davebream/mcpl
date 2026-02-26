package daemon

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/davebream/mcpl/internal/protocol"
)

func rewriteRequestID(msg *protocol.Message, mapper *protocol.IDMapper, sessionID string) uint64 {
	globalID := mapper.Map(msg.ID, sessionID)
	msg.SetID(json.RawMessage(strconv.FormatUint(globalID, 10)))
	return globalID
}

func rewriteResponseID(msg *protocol.Message, mapper *protocol.IDMapper) (string, error) {
	var globalID uint64
	if err := json.Unmarshal(msg.ID, &globalID); err != nil {
		return "", fmt.Errorf("parse global ID: %w", err)
	}

	mapping, ok := mapper.Unmap(globalID)
	if !ok {
		return "", fmt.Errorf("no mapping for global ID %d", globalID)
	}

	msg.SetID(mapping.OriginalID)
	return mapping.SessionID, nil
}

func isInitializeRequest(msg *protocol.Message) bool {
	return msg.IsRequest() && msg.Method == "initialize"
}

func isInitializedNotification(msg *protocol.Message) bool {
	return msg.IsNotification() && msg.Method == "initialized"
}

func isSubscribeRequest(msg *protocol.Message) bool {
	return msg.IsRequest() && msg.Method == "resources/subscribe"
}

func isUnsubscribeRequest(msg *protocol.Message) bool {
	return msg.IsRequest() && msg.Method == "resources/unsubscribe"
}

func isCancellationNotification(msg *protocol.Message) bool {
	return msg.IsNotification() && msg.Method == "notifications/cancelled"
}

// rewriteInitCapabilities replaces the client's capabilities in an initialize
// request with maximal capabilities so the server enables all features.
func rewriteInitCapabilities(msg *protocol.Message) {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}
	params["capabilities"] = json.RawMessage(`{"roots":{"listChanged":true},"sampling":{}}`)
	newParams, err := json.Marshal(params)
	if err != nil {
		return
	}
	msg.Params = newParams
}

// handleCancellation remaps the requestId in a cancellation notification
// from the shim's local ID to the global ID the server knows, then forwards.
func handleCancellation(msg *protocol.Message, mapper *protocol.IDMapper, sessionID string, server *ManagedServer) {
	if len(msg.Params) == 0 {
		return
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}
	reqID, ok := params["requestId"]
	if !ok || len(reqID) == 0 {
		return
	}

	globalID, found := mapper.FindGlobalID(reqID, sessionID)
	if found {
		params["requestId"] = json.RawMessage(strconv.FormatUint(globalID, 10))
		newParams, _ := json.Marshal(params)
		msg.Params = newParams
	}
	// Forward even if mapping not found (response may have already consumed it)
	data, _ := msg.Serialize()
	server.WriteToStdin(data)
}
