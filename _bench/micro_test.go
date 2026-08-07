package main

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/khulnasoft/goactors/actor"
)

// counterActor counts how many *Message it processed.
type counterActor struct {
	count *atomic.Int64
}

func newCounter(count *atomic.Int64) actor.Producer {
	return func() actor.Receiver {
		return &counterActor{count: count}
	}
}

func (c *counterActor) Receive(ctx *actor.Context) {
	switch ctx.Message().(type) {
	case *Message:
		c.count.Add(1)
	}
}

// BenchmarkLocalSend measures the single-engine, single-sender, single-consumer
// send path (bypassing batching effects are NOT removed: messages accumulate).
func BenchmarkLocalSend(b *testing.B) {
	e, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		b.Fatal(err)
	}
	var count atomic.Int64
	e.Spawn(newCounter(&count), "counter")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Send(e.Registry.GetPID("counter", ""), &Message{})
		_ = count
	}
}

// BenchmarkLocalSendDrop measures pure enqueue throughput to a hot actor that
// continually drains, approximating steady-state send cost.
func BenchmarkLocalSendDrop(b *testing.B) {
	e, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		b.Fatal(err)
	}
	pid := e.Spawn(newCounter(&countBase), "counter")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Send(pid, &Message{})
	}
}

var countBase atomic.Int64

// BenchmarkLocalPing measures request/response latency (p99 shown by caller).
func BenchmarkLocalPing(b *testing.B) {
	e, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		b.Fatal(err)
	}
	pid := e.Spawn(newPingPong(), "pong")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := e.Request(pid, &Ping{}, time.Second).Result()
		if err != nil {
			b.Fatal(err)
		}
		if _, ok := res.(*Pong); !ok {
			b.Fatalf("unexpected response: %T", res)
		}
	}
}

type pingPong struct{}

func newPingPong() actor.Producer {
	return func() actor.Receiver { return &pingPong{} }
}

func (p *pingPong) Receive(ctx *actor.Context) {
	switch ctx.Message().(type) {
	case *Ping:
		ctx.Respond(&Pong{})
	}
}

// BenchmarkConcurrentSenders measures throughput with N senders hitting one actor.
func BenchmarkConcurrentSenders(b *testing.B) {
	e, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		b.Fatal(err)
	}
	var count atomic.Int64
	pid := e.Spawn(newCounter(&count), "counter")

	b.ResetTimer()
	b.SetParallelism(8)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			e.Send(pid, &Message{})
		}
	})
	b.StopTimer()

	// Ensure the actor drains before asserting delivery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if count.Load() == int64(b.N) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := count.Load(); got != int64(b.N) {
		b.Fatalf("delivered %d of %d messages", got, b.N)
	}
}

var _ = fmt.Sprintf
var _ sync.WaitGroup