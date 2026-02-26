package daemon

import (
	"bytes"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/davebream/mcpl/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagedServerHasLogger(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg := &config.ServerConfig{Command: "echo"}
	s := NewManagedServer("test", cfg, logger)
	assert.NotNil(t, s.logger)
}

func TestDrainStderr(t *testing.T) {
	// Create a buffer-backed logger to capture output
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	cfg := &config.ServerConfig{Command: "echo"}
	s := NewManagedServer("test", cfg, logger)

	// Create a pipe to simulate server stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)

	// Start draining in background
	done := make(chan struct{})
	go func() {
		s.drainStderr(r)
		close(done)
	}()

	// Write test lines to the pipe
	w.Write([]byte("test error line 1\n"))
	w.Write([]byte("test error line 2\n"))
	w.Close() // signal EOF so drainStderr returns

	<-done

	// Verify the logger captured the lines
	output := buf.String()
	assert.Contains(t, output, "test error line 1")
	assert.Contains(t, output, "test error line 2")
	assert.Contains(t, output, "server stderr")
}

func TestWriteToStdinTimeout(t *testing.T) {
	// Create a pipe where nobody reads the read end — fills buffer, blocks writes
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer r.Close()

	cfg := &config.ServerConfig{Command: "echo"}
	s := NewManagedServer("test", cfg, nil)
	s.mu.Lock()
	s.stdin = w
	s.mu.Unlock()

	// Fill the pipe buffer (typically 64KB on macOS/Linux)
	filler := make([]byte, 128*1024)
	// Write in a goroutine since it will block
	go func() {
		for i := 0; i < 10; i++ {
			w.Write(filler)
		}
	}()

	// Give the filler goroutine time to fill the buffer
	time.Sleep(100 * time.Millisecond)

	// WriteToStdin should return an error within the deadline, not hang forever
	start := time.Now()
	err = s.WriteToStdin([]byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`))
	elapsed := time.Since(start)

	// Should complete within 10s+buffer (the deadline), not hang indefinitely
	assert.Less(t, elapsed, 15*time.Second, "WriteToStdin should not hang indefinitely")
	// After timeout, stdin is closed — further writes should fail
	assert.Error(t, err)
}

func TestManagedServerSerializeQueue(t *testing.T) {
	t.Run("created when serialize is true", func(t *testing.T) {
		cfg := &config.ServerConfig{Command: "echo", Serialize: true}
		s := NewManagedServer("test", cfg, nil)
		assert.NotNil(t, s.serializeQueue, "serialize queue should be created when config.Serialize is true")
		assert.NotNil(t, s.serializeWaiters, "serialize waiters map should be created")
		s.CloseSerializeQueue()
	})

	t.Run("nil when serialize is false", func(t *testing.T) {
		cfg := &config.ServerConfig{Command: "echo"}
		s := NewManagedServer("test", cfg, nil)
		assert.Nil(t, s.serializeQueue, "serialize queue should be nil when config.Serialize is false")
		assert.Nil(t, s.serializeWaiters, "serialize waiters map should be nil")
	})

	t.Run("closed on stop", func(t *testing.T) {
		cfg := &config.ServerConfig{Command: "echo", Serialize: true}
		s := NewManagedServer("test", cfg, nil)
		assert.NotNil(t, s.serializeQueue)
		s.CloseSerializeQueue()
		// No panic = success (queue's processLoop exited)
	})
}

func TestServerState(t *testing.T) {
	t.Run("valid transitions", func(t *testing.T) {
		transitions := []struct {
			from, to ServerState
			valid    bool
		}{
			{StateStopped, StateStarting, true},
			{StateStarting, StateInitializing, true},
			{StateStarting, StateStopped, true}, // start failed
			{StateInitializing, StateReady, true},
			{StateInitializing, StateStopped, true}, // init failed
			{StateReady, StateDraining, true},
			{StateDraining, StateStopped, true},
			{StateDraining, StateReady, true}, // cancel drain
			{StateStopped, StateReady, false},
			{StateReady, StateStarting, false},
			{StateStopped, StateDraining, false},
		}
		for _, tt := range transitions {
			t.Run(tt.from.String()+"->"+tt.to.String(), func(t *testing.T) {
				assert.Equal(t, tt.valid, IsValidTransition(tt.from, tt.to),
					"%s -> %s should be valid=%v", tt.from, tt.to, tt.valid)
			})
		}
	})
}

func TestServerStateString(t *testing.T) {
	assert.Equal(t, "STOPPED", StateStopped.String())
	assert.Equal(t, "STARTING", StateStarting.String())
	assert.Equal(t, "INITIALIZING", StateInitializing.String())
	assert.Equal(t, "READY", StateReady.String())
	assert.Equal(t, "DRAINING", StateDraining.String())
}

func TestManagedServerTransition(t *testing.T) {
	t.Run("successful transition", func(t *testing.T) {
		s := NewManagedServer("test", nil, nil)
		assert.Equal(t, StateStopped, s.State())

		err := s.TransitionTo(StateStarting)
		assert.NoError(t, err)
		assert.Equal(t, StateStarting, s.State())
	})

	t.Run("invalid transition returns error", func(t *testing.T) {
		s := NewManagedServer("test", nil, nil)
		err := s.TransitionTo(StateReady)
		assert.Error(t, err)
		assert.Equal(t, StateStopped, s.State()) // state unchanged
	})
}

func TestManagedServerConnections(t *testing.T) {
	s := NewManagedServer("test", nil, nil)

	s.AddConnection("conn-1")
	s.AddConnection("conn-2")
	assert.Equal(t, 2, s.ConnectionCount())

	s.RemoveConnection("conn-1")
	assert.Equal(t, 1, s.ConnectionCount())

	s.RemoveConnection("conn-2")
	assert.Equal(t, 0, s.ConnectionCount())
}

func TestManagedServerCrashTracking(t *testing.T) {
	s := NewManagedServer("test", nil, nil)

	s.RecordCrash()
	assert.False(t, s.IsFailed())

	s.RecordCrash()
	assert.False(t, s.IsFailed())

	s.RecordCrash()
	assert.True(t, s.IsFailed()) // 3 crashes

	s.ResetCrashes()
	assert.False(t, s.IsFailed())
}
