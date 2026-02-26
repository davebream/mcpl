// mock_mcp_server is a minimal MCP server for integration testing.
// It responds to initialize, tools/list, and echoes other requests back.
// Communicates via newline-delimited JSON on stdin/stdout.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)

	for scanner.Scan() {
		var msg message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			fmt.Fprintf(os.Stderr, "mock: invalid JSON: %v\n", err)
			continue
		}

		var resp message

		switch msg.Method {
		case "initialize":
			resp = message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result: json.RawMessage(`{
					"protocolVersion": "2024-11-05",
					"serverInfo": {"name": "mock-server", "version": "1.0.0"},
					"capabilities": {"tools": {}}
				}`),
			}

		case "initialized":
			// Notification — no response
			continue

		case "tools/list":
			resp = message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result: json.RawMessage(`{
					"tools": [
						{"name": "echo", "description": "Echo back input", "inputSchema": {"type": "object"}}
					]
				}`),
			}

		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			json.Unmarshal(msg.Params, &params)

			if params.Name == "slow-echo" {
				// Sleep 200ms to simulate slow processing
				time.Sleep(200 * time.Millisecond)
			}

			resp = message{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Result:  json.RawMessage(fmt.Sprintf(`{"content": [{"type": "text", "text": %s}]}`, msg.Params)),
			}

		default:
			// Unknown method — return error
			resp = message{
				JSONRPC: "2.0",
				ID:      msg.ID,
			}
			errData, _ := json.Marshal(map[string]interface{}{
				"code":    -32601,
				"message": fmt.Sprintf("method not found: %s", msg.Method),
			})
			resp.Result = nil
			data, _ := json.Marshal(struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
				Error   json.RawMessage `json:"error"`
			}{
				JSONRPC: "2.0",
				ID:      msg.ID,
				Error:   json.RawMessage(errData),
			})
			data = append(data, '\n')
			os.Stdout.Write(data)
			continue
		}

		data, _ := json.Marshal(resp)
		data = append(data, '\n')
		os.Stdout.Write(data)
	}
}
