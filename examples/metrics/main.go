package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/khulnasoft/goactors/actor"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type PromMetrics struct {
	msgCounter prometheus.Counter
	msgLatency prometheus.Histogram
}

func newPromMetrics(prefix string) *PromMetrics {
	msgCounter := promauto.NewCounter(prometheus.CounterOpts{
		Name: fmt.Sprintf("%s_actor_msg_counter", prefix),
		Help: "counter of the messages the actor received",
	})
	msgLatency := promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    fmt.Sprintf("%s_actor_msg_latency", prefix),
		Help:    "actor msg latency",
		Buckets: []float64{0.1, 0.5, 1},
	})
	return &PromMetrics{
		msgCounter: msgCounter,
		msgLatency: msgLatency,
	}
}

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

func (p *PromMetrics) WithMetrics() func(actor.ReceiveFunc) actor.ReceiveFunc {
	return func(next actor.ReceiveFunc) actor.ReceiveFunc {
		return func(c *actor.Context) {
			if rng.Intn(100) < 10 { // Sample 10% of messages
				start := time.Now()
				p.msgCounter.Inc()
				next(c)
				ms = time.Since(start).Seconds()
				p.msgLatency.Observe(ms)
			} else {
				next(c)
			}
		}
	}
}

type Message struct {
	data string
}

type Foo struct{}

func (f *Foo) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
		fmt.Println("foo started")
	case actor.Stopped:
	case Message:
		fmt.Println("received:", msg.data)
	}
}

func newFoo() actor.Receiver {
	return &Foo{}
}

type Bar struct{}

func (f *Bar) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
	case actor.Stopped:
	case Message:
		fmt.Println("received:", msg.data)
	}
}

func newBar() actor.Receiver {
	return &Bar{}
}

func main() {
	promListenAddr := flag.String("promlistenaddr", ":2222", "the listen address of the prometheus http handler")
	flag.Parse()
	go func() {
		http.ListenAndServe(*promListenAddr, promhttp.Handler())
	}()
	e, err := actor.NewEngine(actor.NewEngineConfig())
	if err != nil {
		panic(err)
	}
	var (
		foometrics = newPromMetrics("foo")
		barmetrics = newPromMetrics("bar")
		fooPID     = e.Spawn(newFoo, "foo", actor.WithMiddleware(foometrics.WithMetrics()))
		barPID     = e.Spawn(newBar, "bar", actor.WithMiddleware(barmetrics.WithMetrics()))
	)

	messages := []Message{
		{data: "message to foo"},
		{data: "message to bar"},
	}
	for _, msg := range messages {
		e.SendBatch([]actor.PID{fooPID, barPID}, msg)
	}
}
