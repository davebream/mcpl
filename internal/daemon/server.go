package daemon

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/davebream/mcpl/internal/config"
)

type ServerState int

const (
	StateStopped      ServerState = iota
	StateStarting
	StateInitializing
	StateReady
	StateDraining
)

func (s ServerState) String() string {
	switch s {
	case StateStopped:
		return "STOPPED"
	case StateStarting:
		return "STARTING"
	case StateInitializing:
		return "INITIALIZING"
	case StateReady:
		return "READY"
	case StateDraining:
		return "DRAINING"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", int(s))
	}
}

var validTransitions = map[ServerState]map[ServerState]bool{
	StateStopped:      {StateStarting: true},
	StateStarting:     {StateInitializing: true, StateStopped: true},
	StateInitializing: {StateReady: true, StateStopped: true},
	StateReady:        {StateDraining: true},
	StateDraining:     {StateStopped: true, StateReady: true},
}

func IsValidTransition(from, to ServerState) bool {
	if targets, ok := validTransitions[from]; ok {
		return targets[to]
	}
	return false
}

const maxCrashes = 3
const crashWindow = 60 * time.Second

type ManagedServer struct {
	mu          sync.Mutex
	name        string
	config      *config.ServerConfig
	state       ServerState
	connections map[string]bool
	crashes     []time.Time
	startedAt   time.Time
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.ReadCloser
	stderr      io.ReadCloser
	done        chan struct{}
}

func NewManagedServer(name string, cfg *config.ServerConfig) *ManagedServer {
	return &ManagedServer{
		name:        name,
		config:      cfg,
		state:       StateStopped,
		connections: make(map[string]bool),
	}
}

func (s *ManagedServer) Name() string { return s.name }

func (s *ManagedServer) State() ServerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *ManagedServer) TransitionTo(newState ServerState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !IsValidTransition(s.state, newState) {
		return fmt.Errorf("invalid state transition: %s -> %s", s.state, newState)
	}
	s.state = newState
	if newState == StateStarting {
		s.startedAt = time.Now()
	}
	return nil
}

func (s *ManagedServer) AddConnection(connID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.connections[connID] = true
}

func (s *ManagedServer) RemoveConnection(connID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.connections, connID)
}

func (s *ManagedServer) ConnectionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.connections)
}

func (s *ManagedServer) RecordCrash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.crashes = append(s.crashes, now)
	// Trim crashes outside window
	cutoff := now.Add(-crashWindow)
	filtered := s.crashes[:0]
	for _, t := range s.crashes {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	s.crashes = filtered
}

func (s *ManagedServer) IsFailed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.crashes) < maxCrashes {
		return false
	}
	cutoff := time.Now().Add(-crashWindow)
	count := 0
	for _, t := range s.crashes {
		if t.After(cutoff) {
			count++
		}
	}
	return count >= maxCrashes
}

func (s *ManagedServer) ResetCrashes() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crashes = nil
}

// ForceStop sets state to StateStopped regardless of current state.
// Used for crash recovery when the process exits unexpectedly and normal
// transitions don't cover the current state (e.g., STARTING -> STOPPED is valid,
// but this avoids needing to check current state).
func (s *ManagedServer) ForceStop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = StateStopped
}

func (s *ManagedServer) Start(env map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd := exec.Command(s.config.Command, s.config.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", s.name, err)
	}

	s.cmd = cmd
	s.stdin = stdin
	s.stdout = stdout
	s.stderr = stderr
	s.done = make(chan struct{})

	// Drain stderr to prevent pipe buffer deadlock
	go io.Copy(io.Discard, stderr)

	return nil
}

func (s *ManagedServer) Stop() {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return
	}

	// Kill process group
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)

	// Wait with timeout
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-s.done
	}
}

func (s *ManagedServer) Wait() {
	if s.cmd != nil {
		s.cmd.Wait()
		close(s.done)
	}
}

func (s *ManagedServer) WriteToStdin(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin == nil {
		return fmt.Errorf("server %s stdin not available", s.name)
	}
	line := make([]byte, len(data)+1)
	copy(line, data)
	line[len(data)] = '\n'
	_, err := s.stdin.Write(line)
	return err
}
