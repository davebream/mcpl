package shim

import (
	"bufio"
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
	go func() {
		<-sigCh
		conn.Close()
		os.Exit(0)
	}()

	// Handshake
	req := &protocol.ConnectRequest{MCPL: 1, Type: "connect", Server: serverName}
	data, _ := json.Marshal(req)
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("send handshake: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return fmt.Errorf("read handshake response: connection closed")
	}

	var resp protocol.ConnectResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return fmt.Errorf("parse handshake response: %w", err)
	}

	if resp.Type == "error" {
		return fmt.Errorf("daemon error: [%s] %s", resp.Code, resp.Message)
	}

	// Bidirectional bridge: stdin -> socket, socket -> stdout
	errCh := make(chan error, 2)

	// stdin -> socket
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		errCh <- err
	}()

	// socket -> stdout
	go func() {
		_, err := io.Copy(os.Stdout, conn)
		errCh <- err
	}()

	// Wait for either direction to finish
	<-errCh
	return nil
}
