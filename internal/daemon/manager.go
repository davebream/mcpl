package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/davebream/mcpl/internal/config"
)

type ServerManager struct {
	mu      sync.Mutex
	servers map[string]*ManagedServer
	cfg     *config.Config
	logger  *slog.Logger
}

func NewServerManager(cfg *config.Config, logger *slog.Logger) *ServerManager {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	servers := make(map[string]*ManagedServer)
	for name, scfg := range cfg.Servers {
		servers[name] = NewManagedServer(name, scfg)
	}
	return &ServerManager{
		servers: servers,
		cfg:     cfg,
		logger:  logger,
	}
}

func (m *ServerManager) GetServer(name string) *ManagedServer {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.servers[name]
}

func (m *ServerManager) StartServer(ctx context.Context, name string) error {
	m.mu.Lock()
	server, ok := m.servers[name]
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("unknown server: %s", name)
	}

	if server.IsFailed() {
		return fmt.Errorf("server %s has failed (too many crashes). Use `mcpl restart %s` to reset", name, name)
	}

	if server.State() != StateStopped {
		return nil // already running or starting
	}

	if err := server.TransitionTo(StateStarting); err != nil {
		return err
	}

	resolvedEnv := config.ResolveEnv(server.config.Env)

	if err := server.Start(resolvedEnv); err != nil {
		server.TransitionTo(StateStopped)
		return err
	}

	m.logger.Info("server started", "server", name, "pid", server.cmd.Process.Pid)

	// Monitor process in background
	go func() {
		server.Wait()
		state := server.State()
		if state != StateStopped {
			m.logger.Warn("server exited unexpectedly", "server", name, "state", state)
			server.RecordCrash()
			server.ForceStop()
		}
	}()

	return nil
}

func (m *ServerManager) StopServer(name string) {
	m.mu.Lock()
	server, ok := m.servers[name]
	m.mu.Unlock()

	if !ok {
		return
	}

	if server.State() == StateStopped {
		return
	}

	server.ForceStop()
	server.Stop()

	m.logger.Info("server stopped", "server", name)
}

func (m *ServerManager) StopAll() {
	m.mu.Lock()
	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	m.mu.Unlock()

	for _, name := range names {
		m.StopServer(name)
	}
}
