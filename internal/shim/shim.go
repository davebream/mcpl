package shim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/davebream/mcpl/internal/protocol"
)

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Connect bridges stdin/stdout to the daemon for the named MCP server.
func Connect(serverName, socketPath string) error {
	if isTTY() {
		return fmt.Errorf("mcpl connect is not meant to be run directly.\nUsage: configure your MCP client with {\"command\": \"mcpl\", \"args\": [\"connect\", \"%s\"]}", serverName)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	// Signal handler — close socket cleanly
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		_, ok := <-sigCh
		if !ok {
			return // channel closed, normal exit
		}
		conn.Close()
		os.Exit(0)
	}()

	// Handshake — send request
	req := &protocol.ConnectRequest{MCPL: 1, Type: "connect", Server: serverName}
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal handshake: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("send handshake: %w", err)
	}

	// Read response via bufio.Reader — preserves any pre-buffered bytes
	// for the subsequent io.Copy (avoids losing data the reader pre-read)
	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read handshake response: %w", err)
	}

	var resp protocol.ConnectResponse
	if err := json.Unmarshal(bytes.TrimRight(line, "\n"), &resp); err != nil {
		return fmt.Errorf("parse handshake response: %w", err)
	}

	if resp.Type == "error" {
		return fmt.Errorf("daemon error: [%s] %s", resp.Code, resp.Message)
	}

	// Bidirectional bridge: stdin -> socket, socket -> stdout
	// Use reader (not conn) for socket->stdout to include any pre-buffered bytes
	errCh := make(chan error, 2)

	// stdin -> socket
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		errCh <- err
	}()

	// socket -> stdout (via buffered reader to preserve handshake-buffered data)
	go func() {
		_, err := io.Copy(os.Stdout, reader)
		errCh <- err
	}()

	// Wait for either direction to finish
	<-errCh
	return nil
}
