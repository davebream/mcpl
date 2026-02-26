package daemon

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIdleTracker(t *testing.T) {
	t.Run("not idle when connections exist", func(t *testing.T) {
		tracker := NewIdleTracker(100 * time.Millisecond)
		tracker.ConnectionAdded()
		assert.False(t, tracker.IsIdle())
	})

	t.Run("becomes idle after timeout with no connections", func(t *testing.T) {
		tracker := NewIdleTracker(50 * time.Millisecond)
		tracker.ConnectionAdded()
		tracker.ConnectionRemoved()

		assert.False(t, tracker.IsIdle()) // not yet
		time.Sleep(60 * time.Millisecond)
		assert.True(t, tracker.IsIdle()) // now idle
	})

	t.Run("reset idle timer on new connection", func(t *testing.T) {
		tracker := NewIdleTracker(50 * time.Millisecond)
		tracker.ConnectionRemoved() // start idle
		time.Sleep(30 * time.Millisecond)

		tracker.ConnectionAdded()   // reset
		tracker.ConnectionRemoved() // start idle again

		time.Sleep(30 * time.Millisecond)
		assert.False(t, tracker.IsIdle()) // only 30ms since last removal
	})

	t.Run("idle duration returns zero when active", func(t *testing.T) {
		tracker := NewIdleTracker(100 * time.Millisecond)
		tracker.ConnectionAdded()
		assert.Equal(t, time.Duration(0), tracker.IdleDuration())
	})
}
