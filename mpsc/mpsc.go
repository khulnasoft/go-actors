// Package mpsc provides a lock-free multi-producer, single-consumer queue.
//
// The implementation is based on the Vyukov MPSC queue
// (http://www.1024cores.net/home/lock-free-algorithms/queues/non-intrusive-mpsc-node-based-queue).
// Push is safe to call from any number of goroutines concurrently. PopBatch
// must only ever be called from the single consumer goroutine.
package mpsc

import (
	"sync"
	"sync/atomic"
)

type node[V any] struct {
	next atomic.Pointer[node[V]]
	val  V
}

// Queue is a lock-free unbounded MPSC FIFO queue.
//
// The zero value is NOT usable; construct instances via New.
type Queue[V any] struct {
	// head is only mutated by the single consumer goroutine and points to the
	// first node that has not been consumed yet (the stub/sentinel until the
	// first push). It is atomic so Push can safely read/update it while the
	// consumer walks the list.
	head atomic.Pointer[node[V]]
	// tail is the producer-side insertion point; producers exchange it to
	// append a node.
	tail atomic.Pointer[node[V]]
	// len is the approximate number of pending items (used for cheap Len()).
	len atomic.Int64
	// pool recycles node allocations between producers and the consumer.
	pool sync.Pool
}

// New returns an empty MPSC queue.
func New[V any]() *Queue[V] {
	q := &Queue[V]{}
	q.pool.New = func() any {
		return new(node[V])
	}
	stub := q.pool.Get().(*node[V])
	q.head.Store(stub)
	q.tail.Store(stub)
	return q
}

// Push appends v to the tail of the queue. Safe to call from multiple
// goroutines concurrently.
func (q *Queue[V]) Push(v V) {
	n := q.pool.Get().(*node[V])
	n.val = v
	n.next.Store(nil)

	// Producers serialize on tail: whichever producer wins the swap is the
	// one whose node is immediately preceded by the previous tail.
	prev := q.tail.Swap(n)
	// Release: the node must be fully visible to the consumer before its
	// next pointer is published.
	prev.next.Store(n)
	q.len.Add(1)
}

// Len returns the approximate number of items currently in the queue.
func (q *Queue[V]) Len() int64 {
	return q.len.Load()
}

// PopBatch pops up to n items from the head of the queue into dst, returning
// the filled slice. It returns false if the queue was empty. Must only be
// called from the single consumer goroutine.
func (q *Queue[V]) PopBatch(dst []V, n int) ([]V, bool) {
	head := q.head.Load()
	if n > cap(dst) {
		dst = make([]V, n)
	}
	dst = dst[:n]
	consumed := 0
	cur := head
	for consumed < n {
		next := cur.next.Load()
		if next == nil {
			break
		}
		dst[consumed] = next.val
		// A producer may be in the middle of Push: it has swapped tail but
		// has not yet published prev.next. When that happens the producer
		// itself will observe the queue as non-empty and schedule the
		// consumer again, so it is safe to stop consuming here.
		next.val = zeroVal[V]()
		cur = next
		consumed++
	}
	if consumed == 0 {
		return dst[:0], false
	}
	// Advance the consumer head to the last consumed node. The old head node
	// (and all fully consumed predecessors) are recycled.
	q.head.Store(cur)
	q.len.Add(-int64(consumed))
	// Recycle the consumed sentinel nodes (everything strictly before cur).
	// Only the consumer touches these, so no synchronization is required.
	for c := head; c != cur; {
		freed := c
		c = c.next.Load()
		q.pool.Put(freed)
	}
	return dst[:consumed], true
}

// Pop pops a single item from the head. Must only be called from the single
// consumer goroutine.
func (q *Queue[V]) Pop() (V, bool) {
	head := q.head.Load()
	next := head.next.Load()
	if next == nil {
		var zero V
		return zero, false
	}
	v := next.val
	next.val = zeroVal[V]()
	q.head.Store(next)
	q.len.Add(-1)
	q.pool.Put(head)
	return v, true
}

func zeroVal[V any]() V {
	var z V
	return z
}
