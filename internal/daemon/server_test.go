package daemon

import (
	"testing"

	"github.com/davebream/mcpl/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestManagedServerSerializeQueue(t *testing.T) {
	t.Run("created when serialize is true", func(t *testing.T) {
		cfg := &config.ServerConfig{Command: "echo", Serialize: true}
		s := NewManagedServer("test", cfg)
		assert.NotNil(t, s.serializeQueue, "serialize queue should be created when config.Serialize is true")
		assert.NotNil(t, s.serializeWaiters, "serialize waiters map should be created")
		s.CloseSerializeQueue()
	})

	t.Run("nil when serialize is false", func(t *testing.T) {
		cfg := &config.ServerConfig{Command: "echo"}
		s := NewManagedServer("test", cfg)
		assert.Nil(t, s.serializeQueue, "serialize queue should be nil when config.Serialize is false")
		assert.Nil(t, s.serializeWaiters, "serialize waiters map should be nil")
	})

	t.Run("closed on stop", func(t *testing.T) {
		cfg := &config.ServerConfig{Command: "echo", Serialize: true}
		s := NewManagedServer("test", cfg)
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
		s := NewManagedServer("test", nil)
		assert.Equal(t, StateStopped, s.State())

		err := s.TransitionTo(StateStarting)
		assert.NoError(t, err)
		assert.Equal(t, StateStarting, s.State())
	})

	t.Run("invalid transition returns error", func(t *testing.T) {
		s := NewManagedServer("test", nil)
		err := s.TransitionTo(StateReady)
		assert.Error(t, err)
		assert.Equal(t, StateStopped, s.State()) // state unchanged
	})
}

func TestManagedServerConnections(t *testing.T) {
	s := NewManagedServer("test", nil)

	s.AddConnection("conn-1")
	s.AddConnection("conn-2")
	assert.Equal(t, 2, s.ConnectionCount())

	s.RemoveConnection("conn-1")
	assert.Equal(t, 1, s.ConnectionCount())

	s.RemoveConnection("conn-2")
	assert.Equal(t, 0, s.ConnectionCount())
}

func TestManagedServerCrashTracking(t *testing.T) {
	s := NewManagedServer("test", nil)

	s.RecordCrash()
	assert.False(t, s.IsFailed())

	s.RecordCrash()
	assert.False(t, s.IsFailed())

	s.RecordCrash()
	assert.True(t, s.IsFailed()) // 3 crashes

	s.ResetCrashes()
	assert.False(t, s.IsFailed())
}
