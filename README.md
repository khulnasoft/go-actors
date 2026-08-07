# Goactors - Blazingly Fast, Low-Latency Actors for Golang

[![Go Report Card](https://goreportcard.com/badge/github.com/khulnasoft/goactors)](https://goreportcard.com/report/github.com/khulnasoft/goactors)
![Build Status](https://github.com/khulnasoft/goactors/actions/workflows/build.yml/badge.svg?branch=master)

Goactors is an **ultra-fast actor engine** designed for speed and low-latency applications such as game servers, advertising brokers, and trading engines. Actors run on a lock-free, high-performance core that can process millions of messages per second.

> **Note:** This README was refactored to match the actual API. If any example does not compile, please open an issue.

---

## Table of Contents

- [Features](#features)
- [Installation](#installation)
- [Quickstart](#quickstart)
- [Spawning Actors](#spawning-actors)
- [Remote Actors](#remote-actors)
- [Event Stream](#event-stream)
- [Middleware](#middleware)
- [Logging](#logging)
- [Benchmarks](#benchmarks)
- [Testing](#testing)
- [License](#license)

---

## Features

- **Fault tolerance** - guaranteed message delivery on actor failure via a buffer mechanism
- **Fire & forget and request & response messaging**
- **High-performance dRPC transport layer**
- **Optimized protobufs without reflection**
- **Lightweight and highly customizable**
- **Cluster support** for distributed, self-discovering actors

---

## Installation

```sh
go get github.com/khulnasoft/goactors/...
```

> **Note:** Goactors requires **Go 1.22 or later**.

---

## Quickstart

### Hello World Example

```go
package main

import (
	"fmt"

	"github.com/khulnasoft/goactors/actor"
)

type message struct {
	data string
}

type helloer struct{}

func newHelloer() actor.Receiver {
	return &helloer{}
}

func (h *helloer) Receive(ctx *actor.Context) {
	switch msg := ctx.Message().(type) {
	case actor.Initialized:
		fmt.Println("Helloer initialized")
	case actor.Started:
		fmt.Println("Helloer started")
	case actor.Stopped:
		fmt.Println("Helloer stopped")
	case *message:
		fmt.Println("Hello, world!", msg.data)
	}
}

func main() {
	engine, _ := actor.NewEngine(actor.NewEngineConfig())
	pid := engine.Spawn(newHelloer, "hello")
	engine.Send(pid, &message{data: "Hello, Goactors!"})
}
```

Each actor runs in its own goroutine and processes messages from its inbox in order. Lifecycle messages (`actor.Initialized`, `actor.Started`, `actor.Stopped`) are delivered automatically.

📂 **More examples are available in the [examples](examples/) folder.**

---

## Spawning Actors

#### Default Configuration

```go
e.Spawn(newFoo, "myactorname")
```

#### Passing Constructor Arguments

Producers are `func() actor.Receiver`. Use a closure to capture configuration:

```go
func newCustomResponder(name string) actor.Producer {
	return func() actor.Receiver {
		return &nameResponder{name: name}
	}
}

pid := engine.Spawn(newCustomResponder("Khulnasoft"), "name-responder")
```

#### Custom Configuration

```go
engine.Spawn(newFoo, "myactorname",
	actor.WithMaxRestarts(4),
	actor.WithInboxSize(2048),
)
```

#### Stateless Function Actors

```go
engine.SpawnFunc(func(c *actor.Context) {
	switch c.Message().(type) {
	case actor.Started:
		fmt.Println("Actor started")
	}
}, "foo")
```

---

## Remote Actors

Goactors allows actors to communicate over a network through the `remote` package using **dRPC** and **protobuf serialization**. Remote instances are created with an address and a `remote.Config`, then attached to an engine.

```go
import (
	"crypto/tls"

	"github.com/khulnasoft/goactors/actor"
	"github.com/khulnasoft/goactors/remote"
)

tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
config := remote.NewConfig().WithTLS(tlsConfig)

r := remote.New("0.0.0.0:2222", config)
engine, err := actor.NewEngine(actor.NewEngineConfig().WithRemote(r))
if err != nil {
	// handle error
}
```

When a `remote` is supplied, all messages sent through the engine are transparently routed over the network. See the [Remote Example](examples/remote) and [Chat Server](examples/chat) for full usage.

---

## Event Stream

Goactors provides a **powerful event stream** to handle system events gracefully:

- **Monitor crashes, deadletters, and network failures**
- **Subscribe actors to system events**
- **Broadcast custom events**

Actors subscribe to the event stream by calling `engine.Subscribe(pid)`:

```go
engine.Subscribe(myPID)
```

### Internal Events

| Package | Event |
| --- | --- |
| `actor`  | `ActorInitializedEvent` |
| `actor`  | `ActorStartedEvent` |
| `actor`  | `ActorStoppedEvent` |
| `actor`  | `DeadLetterEvent` |
| `actor`  | `ActorRestartedEvent` |
| `actor`  | `RemoteUnreachableEvent` |
| `cluster` | `MemberJoinEvent` |
| `cluster` | `MemberLeaveEvent` |
| `cluster` | `ActivationEvent` |
| `cluster` | `DeactivationEvent` |

📂 **See the [Event Stream Example](examples/eventstream) for usage.**

---

## Middleware

Extend actors with **custom middleware** for:

- **Metrics collection**
- **Data persistence**
- **Custom logging**

📂 **Examples are available in the [middleware folder](examples/middleware).**

---

## Logging

Goactors uses **structured logging** via `log/slog`:

```go
import "log/slog"

slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
```

---

## Benchmarks

The benchmark builds several remote engines, spawns a large number of actors, and floods them with messages to measure messages processed per second.

```sh
make bench
```

```
spawned 10 engines
spawned 2000 actors per engine
Send storm starting, will send for 10s using 20 workers
Messages sent per second 1333665
...
Messages sent per second 677231
Concurrent senders: 20 messages sent 6114914, messages received 6114914 - duration: 10s
messages per second: 611491
deadletters: 0
```

Since the default workload is large, a smaller run can be used on machines with few CPUs or limited memory:

```sh
BENCH_ENGINES=4 BENCH_ACTORS_PER_ENGINE=300 BENCH_SENDERS=2 BENCH_DURATION=4 make bench
```

| Variable | Default | Description |
| --- | --- | --- |
| `BENCH_ENGINES` | `10` | Number of remote engines |
| `BENCH_ACTORS_PER_ENGINE` | `2000` | Actors spawned per engine |
| `BENCH_SENDERS` | `20` | Concurrent senders |
| `BENCH_DURATION` | `10` | Storm duration (seconds) |

---

## Testing

```sh
make test
```

---

## License

Goactors is licensed under the **MIT License**.