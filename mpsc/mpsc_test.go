package mpsc

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestSingleProducerFIFO ensures strict FIFO ordering for a single producer.
func TestSingleProducerFIFO(t *testing.T) {
	q := New[int]()
	const n = 100_000
	for i := 0; i < n; i++ {
		q.Push(i)
	}
	got := 0
	for {
		batch, ok := q.PopBatch(nil, 1024)
		if !ok {
			break
		}
		for _, v := range batch {
			if v != got {
				t.Fatalf("expected %d got %d", got, v)
			}
			got++
		}
	}
	if got != n {
		t.Fatalf("expected %d items, got %d", n, got)
	}
}

// TestConcurrentNoLoss verifies that under many concurrent producers every
// pushed value is received exactly once.
func TestConcurrentNoLoss(t *testing.T) {
	q := New[int]()
	const producers = 16
	const perProducer = 50_000
	var pushed atomic.Int64

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				q.Push(1)
			}
		}()
	}
	go func() {
		wg.Wait()
		pushed.Store(1)
	}()

	var received int64
	for {
		batch, ok := q.PopBatch(nil, 2048)
		if ok {
			received += int64(len(batch))
		}
		if pushed.Load() == 1 && q.Len() == 0 {
			break
		}
	}
	if want := int64(producers * perProducer); received != want {
		t.Fatalf("expected %d items, received %d", want, received)
	}
}

// TestConcurrentUniqueValues ensures exactly-once delivery of unique values.
func TestConcurrentUniqueValues(t *testing.T) {
	q := New[int]()
	const producers = 8
	const perProducer = 20_000
	var wg sync.WaitGroup

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < perProducer; i++ {
				q.Push(base*perProducer + i)
			}
		}(p)
	}
	go wg.Wait()

	seen := make(map[int]struct{}, producers*perProducer)
	for {
		batch, ok := q.PopBatch(nil, 2048)
		if ok {
			for _, v := range batch {
				if _, dup := seen[v]; dup {
					t.Fatalf("duplicate value %d", v)
				}
				seen[v] = struct{}{}
			}
		}
		if q.Len() == 0 && allProducersDone(&wg) {
			break
		}
	}
	if want := producers * perProducer; len(seen) != want {
		t.Fatalf("expected %d unique values, got %d", want, len(seen))
	}
}

func allProducersDone(wg *sync.WaitGroup) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	default:
		return false
	}
}
