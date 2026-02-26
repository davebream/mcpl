package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSerializeQueueCloseWaits(t *testing.T) {
	q := NewSerializeQueue()

	// Enqueue a slow task
	started := make(chan struct{})
	q.Enqueue(func() {
		close(started)
		time.Sleep(200 * time.Millisecond)
	})

	<-started // wait for task to begin

	// Close should block until processLoop exits (slow task finishes)
	done := make(chan struct{})
	go func() {
		q.Close()
		close(done)
	}()

	select {
	case <-done:
		// Close returned — processLoop exited cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("Close() should return after processLoop exits, not hang")
	}
}

func TestSerializeQueueEnqueueAfterClose(t *testing.T) {
	q := NewSerializeQueue()
	q.Close()

	// Enqueue after close should not panic and should not execute
	executed := false
	q.Enqueue(func() {
		executed = true
	})

	time.Sleep(50 * time.Millisecond)
	assert.False(t, executed, "function should not execute after queue is closed")
}

func TestSerializeQueue(t *testing.T) {
	t.Run("processes requests sequentially", func(t *testing.T) {
		q := NewSerializeQueue()
		defer q.Close()

		var order []int
		var mu sync.Mutex

		for i := 0; i < 3; i++ {
			i := i
			q.Enqueue(func() {
				mu.Lock()
				order = append(order, i)
				mu.Unlock()
				time.Sleep(10 * time.Millisecond)
			})
		}

		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		assert.Equal(t, []int{0, 1, 2}, order)
		mu.Unlock()
	})

	t.Run("cancel dequeues without executing", func(t *testing.T) {
		q := NewSerializeQueue()
		defer q.Close()

		// Block the processLoop so the cancellable entry stays queued
		blocker := make(chan struct{})
		q.Enqueue(func() {
			<-blocker
		})

		executed := false
		id := q.EnqueueCancellable(func() {
			executed = true
		})

		// Cancel while blocker holds the processLoop
		q.Cancel(id)
		// Release the blocker
		close(blocker)

		time.Sleep(50 * time.Millisecond)
		assert.False(t, executed)
	})
}
