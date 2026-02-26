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
	progressTokens map[string]string              // progressToken -> sessionID
	pendingInit    map[uint64]string              // globalID -> serverName (for caching init responses)
	pendingFanout  map[uint64]*rootsAggregator    // fanout ID -> aggregator
}

func New(cfg *config.Config, socketPath string, logger *slog.Logger) (*Daemon, error) {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	servers := make(map[string]*ManagedServer)
	for name, scfg := range cfg.Servers {
		if scfg.IsManaged() {
			servers[name] = NewManagedServer(name, scfg, logger.With("server", name))
		}
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
		pendingInit:    make(map[uint64]string),
		pendingFanout:  make(map[uint64]*rootsAggregator),
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

	// Close all shim connections (stops new requests from entering sessionLoop)
	d.mu.Lock()
	for _, session := range d.sessions {
		session.Close()
	}
	d.pendingInit = make(map[uint64]string)
	d.pendingFanout = make(map[uint64]*rootsAggregator)
	d.mu.Unlock()

	// Close serialize queues (closes waiters first, then waits for processLoop exit)
	d.mu.Lock()
	servers := make([]*ManagedServer, 0, len(d.servers))
	for _, server := range d.servers {
		servers = append(servers, server)
	}
	d.mu.Unlock()
	for _, server := range servers {
		server.CloseSerializeQueue()
	}

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

	// Auto-start server subprocess if not running
	if server.State() == StateStopped {
		if err := d.ensureServerRunning(server); err != nil {
			server.RemoveConnection(session.ID)
			d.mu.Lock()
			delete(d.sessions, session.ID)
			d.mu.Unlock()
			d.writeError(conn, "start_failed", fmt.Sprintf("failed to start server %q: %v", req.Server, err))
			return
		}
	}

	status := "ready"
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
	// Remove progress tokens owned by this session
	for token, sid := range d.progressTokens {
		if sid == session.ID {
			delete(d.progressTokens, token)
		}
	}
	d.mu.Unlock()
	orphanedURIs := d.subs.RemoveSession(session.ID)
	for _, uri := range orphanedURIs {
		// Send resources/unsubscribe to server for URIs with no remaining subscribers
		unsubMsg := &protocol.Message{
			JSONRPC: "2.0",
			ID:      json.RawMessage(fmt.Sprintf("%d", d.idMapper.NextID())),
			Method:  "resources/unsubscribe",
			Params:  json.RawMessage(fmt.Sprintf(`{"uri":%q}`, uri)),
		}
		data, _ := unsubMsg.Serialize()
		server.WriteToStdin(data)
	}

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

		// Intercept fan-out responses (e.g., roots/list) before they reach the server
		if msg.IsResponse() {
			var fanoutID uint64
			if json.Unmarshal(msg.ID, &fanoutID) == nil {
				d.mu.Lock()
				agg, isFanout := d.pendingFanout[fanoutID]
				if isFanout {
					delete(d.pendingFanout, fanoutID)
				}
				d.mu.Unlock()

				if isFanout {
					if allDone := agg.collect(msg.Result); allDone {
						agg.finalize()
					}
					continue
				}
			}
			// Not a fan-out response — forward to server as before
			data, _ := msg.Serialize()
			server.WriteToStdin(data)
			continue
		}

		isInit := isInitializeRequest(msg)

		// Intercept initialize request — return cached response if available
		if isInit {
			// Parse and store client capabilities regardless of cache status
			session.Capabilities = ParseClientCapabilities(msg.Params)
			d.logger.Debug("session capabilities",
				"session", session.ID,
				"roots", session.Capabilities.Roots,
				"sampling", session.Capabilities.Sampling,
			)

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
			// First shim: rewrite capabilities to maximal before forwarding
			rewriteInitCapabilities(msg)
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

		// Track resource subscriptions — reference-count to avoid premature server unsubscribe.
		// Only forward subscribe to server on first subscriber, unsubscribe on last.
		if isSubscribeRequest(msg) {
			if uri, ok := protocol.ExtractResourceURI(msg); ok {
				count := d.subs.Subscribe(uri, session.ID)
				if count > 1 {
					// Server already subscribed — respond directly, don't forward
					resp := &protocol.Message{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result:  json.RawMessage(`{}`),
					}
					data, _ := resp.Serialize()
					session.WriteLine(data)
					continue
				}
			}
		}
		if isUnsubscribeRequest(msg) {
			if uri, ok := protocol.ExtractResourceURI(msg); ok {
				count := d.subs.Unsubscribe(uri, session.ID)
				if count > 0 {
					// Other sessions still subscribed — respond directly, don't forward
					resp := &protocol.Message{
						JSONRPC: "2.0",
						ID:      msg.ID,
						Result:  json.RawMessage(`{}`),
					}
					data, _ := resp.Serialize()
					session.WriteLine(data)
					continue
				}
			}
		}

		// Handle cancellation — remap requestId before forwarding
		if isCancellationNotification(msg) {
			handleCancellation(msg, d.idMapper, session.ID, server)
			continue
		}

		// Remap request ID and forward to server
		var globalID uint64
		if msg.IsRequest() {
			globalID = rewriteRequestID(msg, d.idMapper, session.ID)
		}

		// Track initialize request for caching the response
		if isInit {
			d.mu.Lock()
			d.pendingInit[globalID] = session.ServerName
			d.mu.Unlock()
		}

		data, _ := msg.Serialize()
		if server.serializeQueue != nil && msg.IsRequest() {
			gid := globalID // capture for closure (globalID is loop-scoped)
			done := make(chan struct{})
			server.mu.Lock()
			server.serializeWaiters[gid] = done
			server.mu.Unlock()

			server.serializeQueue.Enqueue(func() {
				if err := server.WriteToStdin(data); err != nil {
					d.logger.Warn("serialize write failed", "server", server.name, "error", err)
					// Clean up waiter — response won't arrive
					server.mu.Lock()
					if ch, ok := server.serializeWaiters[gid]; ok {
						delete(server.serializeWaiters, gid)
						close(ch)
					}
					server.mu.Unlock()
					return
				}
				<-done // block until response arrives (or server crashes)
			})
		} else {
			server.WriteToStdin(data)
		}
	}
}

// ensureServerRunning starts the server subprocess if it's stopped,
// resolves env vars, transitions state, and starts the stdout reader.
func (d *Daemon) ensureServerRunning(server *ManagedServer) error {
	if server.IsFailed() {
		return fmt.Errorf("server %s has failed (too many crashes)", server.name)
	}

	if err := server.TransitionTo(StateStarting); err != nil {
		return err
	}

	resolvedEnv := config.ResolveEnv(server.config.Env)
	if err := server.Start(resolvedEnv); err != nil {
		server.ForceStop()
		return err
	}

	d.logger.Info("server started", "server", server.name)

	// Start reading server stdout
	d.startServerReader(server)

	// Monitor process in background
	go func() {
		server.Wait()
		state := server.State()
		if state != StateStopped {
			d.logger.Warn("server exited unexpectedly", "server", server.name, "state", state)
			server.RecordCrash()
			server.ForceStop()
			// Close all pending serialize waiters — unblocks processLoop
			server.mu.Lock()
			for id, done := range server.serializeWaiters {
				close(done)
				delete(server.serializeWaiters, id)
			}
			server.mu.Unlock()
			// Clear pending init entries for this server
			d.mu.Lock()
			for id, name := range d.pendingInit {
				if name == server.name {
					delete(d.pendingInit, id)
				}
			}
			d.mu.Unlock()
		}
	}()

	// Transition to ready (skip INITIALIZING for now — first shim handles init handshake)
	if err := server.TransitionTo(StateInitializing); err != nil {
		return err
	}
	if err := server.TransitionTo(StateReady); err != nil {
		return err
	}

	return nil
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

	// Add new servers (skip unmanaged)
	for name, scfg := range newCfg.Servers {
		if _, exists := d.servers[name]; !exists && scfg.IsManaged() {
			d.servers[name] = NewManagedServer(name, scfg, d.logger.With("server", name))
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
