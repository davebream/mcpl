package daemon

import (
	"sync"
	"time"
)

// IdleTracker tracks connection activity and determines whether an entity
// (server or daemon) has been idle long enough to trigger shutdown.
// A freshly created tracker starts the idle clock immediately — if no
// connections arrive within the timeout, IsIdle returns true.
// It is safe for concurrent use.
type IdleTracker struct {
	mu          sync.Mutex
	timeout     time.Duration
	connections int
	idleSince   time.Time
}

func NewIdleTracker(timeout time.Duration) *IdleTracker {
	return &IdleTracker{
		timeout:   timeout,
		idleSince: time.Now(),
	}
}

func (t *IdleTracker) ConnectionAdded() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connections++
	t.idleSince = time.Time{} // not idle
}

func (t *IdleTracker) ConnectionRemoved() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.connections--
	if t.connections <= 0 {
		t.connections = 0
		t.idleSince = time.Now()
	}
}

func (t *IdleTracker) IsIdle() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.connections > 0 {
		return false
	}
	if t.idleSince.IsZero() {
		return false
	}
	return time.Since(t.idleSince) >= t.timeout
}

func (t *IdleTracker) IdleDuration() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.connections > 0 || t.idleSince.IsZero() {
		return 0
	}
	return time.Since(t.idleSince)
}
