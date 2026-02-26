package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/davebream/mcpl/internal/config"
	"github.com/davebream/mcpl/internal/protocol"
)

type Daemon struct {
	cfg            *config.Config
	socketPath     string
	listener       net.Listener
	logger         *slog.Logger
	sessions       map[string]*Session
	servers        map[string]*ManagedServer
	mu             sync.Mutex
	idMapper       *protocol.IDMapper
	initCache      *protocol.InitCache
	subs           *protocol.SubscriptionTracker
	progressTokens map[string]string // progressToken -> sessionID
}

func New(cfg *config.Config, socketPath string, logger *slog.Logger) (*Daemon, error) {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	servers := make(map[string]*ManagedServer)
	for name, scfg := range cfg.Servers {
		servers[name] = NewManagedServer(name, scfg)
	}

	return &Daemon{
		cfg:            cfg,
		socketPath:     socketPath,
		logger:         logger,
		sessions:       make(map[string]*Session),
		servers:        servers,
		idMapper:       protocol.NewIDMapper(),
		initCache:      protocol.NewInitCache(),
		subs:           protocol.NewSubscriptionTracker(),
		progressTokens: make(map[string]string),
	}, nil
}

func (d *Daemon) Run(ctx context.Context) error {
	socketDir := filepath.Dir(d.socketPath)

	if err := config.EnsureDir(socketDir, 0700); err != nil {
		return fmt.Errorf("create socket dir: %w", err)
	}

	// Verify socket directory ownership and permissions
	dirInfo, err := os.Stat(socketDir)
	if err != nil {
		return fmt.Errorf("stat socket dir: %w", err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0077 != 0 {
		return fmt.Errorf("socket directory %s has insecure permissions %o (expected 0700)", socketDir, perm)
	}

	// Remove stale socket only if it cannot be connected to
	if conn, err := net.DialTimeout("unix", d.socketPath, 200*time.Millisecond); err == nil {
		conn.Close()
		return fmt.Errorf("another daemon is already listening on %s", d.socketPath)
	}
	os.Remove(d.socketPath)

	d.listener, err = net.Listen("unix", d.socketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if err := os.Chmod(d.socketPath, 0600); err != nil {
		d.listener.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}

	d.logger.Info("daemon started", "socket", d.socketPath)

	go func() {
		<-ctx.Done()
		d.listener.Close()
	}()

	for {
		conn, err := d.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				d.shutdown()
				return nil
			}
			d.logger.Error("accept error", "error", err)
			continue
		}
		go d.handleConnection(conn)
	}
}

// shutdown performs graceful cleanup: close sessions, stop servers, remove socket+PID files.
func (d *Daemon) shutdown() {
	d.logger.Info("shutting down")

	// Close all shim connections
	d.mu.Lock()
	for _, session := range d.sessions {
		session.Close()
	}
	d.mu.Unlock()

	// Stop all server subprocesses (SIGTERM, then SIGKILL after 10s)
	// (delegated to ServerManager in the full wiring)

	// Remove socket and PID files
	os.Remove(d.socketPath)
	if pidPath, err := config.PIDFilePath(); err == nil {
		os.Remove(pidPath)
	}
}

func (d *Daemon) handleConnection(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 10*1024*1024) // 10MB max message size
	if !scanner.Scan() {
		return
	}

	var req protocol.ConnectRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		d.writeError(conn, "invalid_request", "invalid handshake JSON")
		return
	}

	if err := protocol.ValidateHandshake(&req, 1); err != nil {
		d.writeError(conn, "protocol_error", err.Error())
		return
	}

	// Hot-reload config on each connect handshake
	d.reloadConfig()

	d.mu.Lock()
	server, ok := d.servers[req.Server]
	d.mu.Unlock()

	if !ok {
		d.writeError(conn, "unknown_server", fmt.Sprintf("server %q not found in config", req.Server))
		return
	}

	session := NewSession(conn, req.Server)

	d.mu.Lock()
	d.sessions[session.ID] = session
	d.mu.Unlock()

	server.AddConnection(session.ID)

	status := "ready"
	if server.State() == StateStopped {
		status = "starting"
	}

	resp := protocol.NewConnectedResponse(status)
	session.WriteJSON(resp)

	d.logger.Info("session connected",
		"session", session.ID,
		"server", req.Server,
		"status", status,
	)

	// Message forwarding loop — reads from shim, forwards to server
	// Full implementation in Task 10
	d.sessionLoop(session, server)

	// Cleanup
	server.RemoveConnection(session.ID)
	d.mu.Lock()
	delete(d.sessions, session.ID)
	d.mu.Unlock()

	d.logger.Info("session disconnected", "session", session.ID, "server", req.Server)
}

func (d *Daemon) sessionLoop(session *Session, server *ManagedServer) {
	for {
		line, err := session.ReadLine()
		if err != nil {
			return // shim disconnected
		}

		msg, err := protocol.ParseMessage(line)
		if err != nil {
			d.logger.Warn("invalid message from shim", "session", session.ID, "error", err)
			continue
		}

		// Intercept initialize request — return cached response if available
		if isInitializeRequest(msg) {
			if cached, ok := d.initCache.Get(session.ServerName); ok {
				resp := &protocol.Message{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Result:  cached,
				}
				data, _ := resp.Serialize()
				session.WriteLine(data)
				continue
			}
			// First shim: forward to server, cache response (handled in server reader)
		}

		// Intercept initialized notification — drop if server already initialized
		if isInitializedNotification(msg) {
			if _, ok := d.initCache.Get(session.ServerName); ok {
				continue // already initialized, drop
			}
			// First shim: forward to server
		}

		// Track progress tokens for routing
		if msg.IsRequest() {
			if token, ok := protocol.ExtractProgressToken(msg); ok {
				d.mu.Lock()
				d.progressTokens[token] = session.ID
				d.mu.Unlock()
			}
		}

		// Track resource subscriptions
		if isSubscribeRequest(msg) {
			if uri, ok := extractSubscriptionURI(msg); ok {
				d.subs.Subscribe(uri, session.ID)
			}
		}
		if isUnsubscribeRequest(msg) {
			if uri, ok := extractSubscriptionURI(msg); ok {
				d.subs.Unsubscribe(uri, session.ID)
			}
		}

		// Handle cancellation — forward to server
		if isCancellationNotification(msg) {
			data, _ := msg.Serialize()
			server.WriteToStdin(data)
			continue
		}

		// Remap request ID and forward to server
		if msg.IsRequest() {
			rewriteRequestID(msg, d.idMapper, session.ID)
		}

		data, _ := msg.Serialize()
		server.WriteToStdin(data)
	}
}

func extractSubscriptionURI(msg *protocol.Message) (string, bool) {
	if len(msg.Params) == 0 {
		return "", false
	}
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil || params.URI == "" {
		return "", false
	}
	return params.URI, true
}

// reloadConfig re-reads config.json and adds any new servers to the server map.
// TODO: detect changed/removed servers for lazy restart and drain.
func (d *Daemon) reloadConfig() {
	cfgPath, err := config.ConfigFilePath()
	if err != nil {
		d.logger.Warn("config reload: path error", "error", err)
		return
	}
	newCfg, err := config.Load(cfgPath)
	if err != nil {
		d.logger.Warn("config reload: load error", "error", err)
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Add new servers
	for name, scfg := range newCfg.Servers {
		if _, exists := d.servers[name]; !exists {
			d.servers[name] = NewManagedServer(name, scfg)
			d.logger.Info("config reload: added server", "server", name)
		}
	}
	d.cfg = newCfg
}

func (d *Daemon) writeError(conn net.Conn, code, message string) {
	resp := protocol.NewErrorResponse(code, message)
	data, _ := json.Marshal(resp)
	data = append(data, '\n')
	conn.Write(data)
}
