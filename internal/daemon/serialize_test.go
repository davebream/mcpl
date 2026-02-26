package daemon

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

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

		executed := false
		id := q.EnqueueCancellable(func() {
			executed = true
		})

		q.Cancel(id)
		time.Sleep(50 * time.Millisecond)
		assert.False(t, executed)
	})
}
