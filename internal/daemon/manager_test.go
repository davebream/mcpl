package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/davebream/mcpl/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerManagerStartServer(t *testing.T) {
	t.Run("starts a simple process", func(t *testing.T) {
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{
				"echo-server": {Command: "/bin/cat"}, // cat reads stdin, echoes to stdout
			},
		}
		mgr := NewServerManager(cfg, nil)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := mgr.StartServer(ctx, "echo-server")
		require.NoError(t, err)

		server := mgr.GetServer("echo-server")
		require.NotNil(t, server)
		assert.Equal(t, StateStarting, server.State())

		mgr.StopServer("echo-server")
	})

	t.Run("returns error for unknown server", func(t *testing.T) {
		cfg := &config.Config{Servers: map[string]*config.ServerConfig{}}
		mgr := NewServerManager(cfg, nil)

		err := mgr.StartServer(context.Background(), "nonexistent")
		assert.Error(t, err)
	})

	t.Run("returns error for failed server", func(t *testing.T) {
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{
				"bad": {Command: "/bin/cat"},
			},
		}
		mgr := NewServerManager(cfg, nil)
		server := mgr.GetServer("bad")

		// Simulate 3 crashes
		server.RecordCrash()
		server.RecordCrash()
		server.RecordCrash()

		err := mgr.StartServer(context.Background(), "bad")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed")
	})
}

func TestServerManagerStopServer(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"sleeper": {Command: "/bin/sleep", Args: []string{"60"}},
		},
	}
	mgr := NewServerManager(cfg, nil)

	ctx := context.Background()
	err := mgr.StartServer(ctx, "sleeper")
	require.NoError(t, err)

	// Give process time to start
	time.Sleep(50 * time.Millisecond)

	mgr.StopServer("sleeper")

	server := mgr.GetServer("sleeper")
	// Should be stopped (or transitioning)
	assert.Eventually(t, func() bool {
		return server.State() == StateStopped
	}, 2*time.Second, 50*time.Millisecond)
}
