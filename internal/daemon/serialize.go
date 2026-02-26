package daemon

import (
	"sync"
	"sync/atomic"
)

type SerializeQueue struct {
	mu     sync.Mutex
	queue  []queueEntry
	notify chan struct{}
	closed chan struct{}
	nextID atomic.Uint64
	wg     sync.WaitGroup
}

type queueEntry struct {
	id        uint64
	fn        func()
	cancelled bool
}

func NewSerializeQueue() *SerializeQueue {
	q := &SerializeQueue{
		notify: make(chan struct{}, 1),
		closed: make(chan struct{}),
	}
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		q.processLoop()
	}()
	return q
}

func (q *SerializeQueue) Enqueue(fn func()) {
	select {
	case <-q.closed:
		return // queue shut down, discard
	default:
	}
	q.mu.Lock()
	q.queue = append(q.queue, queueEntry{fn: fn})
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *SerializeQueue) EnqueueCancellable(fn func()) uint64 {
	select {
	case <-q.closed:
		return 0 // queue shut down, discard
	default:
	}
	id := q.nextID.Add(1)
	q.mu.Lock()
	q.queue = append(q.queue, queueEntry{id: id, fn: fn})
	q.mu.Unlock()
	select {
	case q.notify <- struct{}{}:
	default:
	}
	return id
}

func (q *SerializeQueue) Cancel(id uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.queue {
		if q.queue[i].id == id {
			q.queue[i].cancelled = true
			return
		}
	}
}

func (q *SerializeQueue) processLoop() {
	for {
		select {
		case <-q.closed:
			return
		case <-q.notify:
		}

		for {
			select {
			case <-q.closed:
				return
			default:
			}

			q.mu.Lock()
			if len(q.queue) == 0 {
				q.mu.Unlock()
				break
			}
			entry := q.queue[0]
			q.queue[0] = queueEntry{} // zero slot so fn closure can be GC'd
			q.queue = q.queue[1:]
			q.mu.Unlock()

			if !entry.cancelled {
				entry.fn()
			}
		}
	}
}

func (q *SerializeQueue) Close() {
	close(q.closed)
	q.wg.Wait() // block until processLoop exits
}
