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
