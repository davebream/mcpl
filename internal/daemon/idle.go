package daemon

import (
	"sync"
	"time"
)

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
