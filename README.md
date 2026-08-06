# Goactors - Blazingly Fast, Low-Latency Actors for Golang

[![Go Report Card](https://goreportcard.com/badge/github.com/khulnasoft/goactors)](https://goreportcard.com/report/github.com/khulnasoft/goactors)
![Build Status](https://github.com/khulnasoft/goactors/actions/workflows/build.yml/badge.svg?branch=master)

Goactors is an **ultra-fast actor engine** designed for speed and low-latency applications such as game servers, advertising brokers, and trading engines. It can handle **10 million messages in under 1 second**.

---

## 🚀 Features

✅ **Guaranteed message delivery** on actor failure (buffer mechanism)  
✅ **Fire & forget, request & response messaging** supported  
✅ **High-performance dRPC transport layer**  
✅ **Optimized protobufs without reflection**  
✅ **Lightweight and highly customizable**  
✅ **WASM Compilation:** Supports `GOOS=js` and `GOOS=wasm32`  
✅ **Cluster support** for distributed, self-discovering actors  

---

## 🔥 Benchmarks

```sh
make bench
```

```
spawned 10 engines
spawned 2000 actors per engine
Send storm starting, will send for 10s using 20 workers
Messages sent per second 1333665
..
Messages sent per second 677231
Concurrent senders: 20 messages sent 6114914, messages received 6114914 - duration: 10s
messages per second: 611491
deadletters: 0
```

---

## 📦 Installation

```sh
go get github.com/khulnasoft/goactors/...
```

> **Note:** Goactors requires **Golang `1.21`**

---

## 🚀 Quickstart

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

📂 **More examples are available in the [examples](examples/) folder.**

---

## 🛠 Spawning Actors

#### Default Configuration
```go
e.Spawn(newFoo, "myactorname")
```

#### Passing Arguments to Actor Constructor
```go
func newCustomNameResponder(name string) actor.Producer {
	return func() actor.Receiver {
		return &nameResponder{name}
	}
}
```

```go
pid := engine.Spawn(newCustomNameResponder("Khulnasoft"), "name-responder")
```

#### Custom Configuration
```go
e.Spawn(newFoo, "myactorname",
	actor.WithMaxRestarts(4),
	actor.WithInboxSize(2048),
)
```

#### Stateless Function Actors
```go
e.SpawnFunc(func(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Started:
		fmt.Println("Actor started")
	}
}, "foo")
```

---

## 🌍 Remote Actors

Goactors allows actors to communicate over a network using the **Remote** package with **protobuf serialization**.

#### Example Configuration
```go
import "crypto/tls"

tlsConfig := &tls.Config{Certificates: []tls.Certificate{cert}}
config := remote.NewConfig().WithTLS(tlsConfig)
remote := remote.New("0.0.0.0:2222", config)
engine, _ := actor.NewEngine(actor.NewEngineConfig().WithRemote(remote))
```

📂 **Check out the [Remote Actor Examples](examples/remote) and [Chat Server](examples/chat) for details.**

---

## 🎯 Event Stream

Goactors provides a **powerful event stream** to handle system events gracefully:

✅ **Monitor crashes, deadletters, and network failures**  
✅ **Subscribe actors to system events**  
✅ **Broadcast custom events**  

#### List of Internal Events:
- `actor.ActorInitializedEvent`
- `actor.ActorStartedEvent`
- `actor.ActorStoppedEvent`
- `actor.DeadLetterEvent`
- `actor.ActorRestartedEvent`
- `actor.RemoteUnreachableEvent`
- `cluster.MemberJoinEvent`
- `cluster.MemberLeaveEvent`
- `cluster.ActivationEvent`
- `cluster.DeactivationEvent`

📂 **See the [Event Stream Example](examples/eventstream-monitor) for usage.**

---

## ⚙️ Customizing the Engine

Use **function options** to customize the Goactors engine:
```go
r := remote.New(remote.Config{ListenAddr: "0.0.0.0:2222"})
engine, _ := actor.NewEngine(actor.EngineOptRemote(r))
```

---

## 🏗 Middleware

Extend actors with **custom middleware** for:
- **Metrics collection**
- **Data persistence**
- **Custom logging**

📂 **Examples available in the [middleware folder](examples/middleware).**

---

## 📝 Logging

Goactors uses **structured logging** via `log/slog`:
```go
import "log/slog"
slog.SetDefaultLogger(myCustomLogger)
```

---

## ✅ Testing
```sh
make test
```

---

## 📜 License

Goactors is licensed under the **MIT License**.

---

## 📚 Hierarchical Supervision Strategy

Goactors supports a hierarchical supervision strategy where parent actors can supervise their child actors. If a child actor fails, the parent actor can decide whether to restart the child, escalate the failure, or stop the child. This can be implemented by adding supervision policies to the parent actor's context and handling child actor failures accordingly.

### Example
```go
parentPID := e.SpawnFunc(func(c *actor.Context) {
	switch c.Message().(type) {
	case actor.Started:
		child := c.SpawnChildFunc(func(childCtx *actor.Context) {
			switch childCtx.Message().(type) {
			case actor.Started:
				childCtx.Send(childCtx.PID(), "fail")
			case string:
				panic("child actor failure")
			}
		}, "child")
		c.engine.Subscribe(child)
	case actor.ActorRestartedEvent:
		// Handle child actor restart
	}
}, "parent")
```

---

## 🔄 Customizable Restart Policies

Goactors allows actors to have customizable restart policies based on the type of failure. For example, actors can have different restart strategies for different types of errors, such as immediate restart, exponential backoff, or a fixed delay. This can be implemented by adding a configuration option to the actor's context and handling restarts based on the specified policy.

### Example
```go
parentPID := e.SpawnFunc(func(c *actor.Context) {
	switch c.Message().(type) {
	case actor.Started:
		child := c.SpawnChildFunc(func(childCtx *actor.Context) {
			switch childCtx.Message().(type) {
			case actor.Started:
				childCtx.Send(childCtx.PID(), "fail")
			case string:
				panic("child actor failure")
			}
		}, "child", actor.WithRestartPolicy(actor.ExponentialBackoff))
		c.engine.Subscribe(child)
	case actor.ActorRestartedEvent:
		// Handle child actor restart
	}
}, "parent")
```

---

## 🩺 Actor Health Monitoring

Goactors provides a health monitoring system for actors to detect and handle unhealthy actors. Periodically check the health of actors and take appropriate actions, such as restarting or stopping unhealthy actors. This can be implemented by adding a health check mechanism to the actor's context and scheduling periodic health checks using the existing `SendRepeat` method.

### Example
```go
parentPID := e.SpawnFunc(func(c *actor.Context) {
	switch c.Message().(type) {
	case actor.Started:
		child := c.SpawnChildFunc(func(childCtx *actor.Context) {
			switch childCtx.Message().(type) {
			case actor.Started:
				childCtx.EnableHealthCheck(time.Millisecond*10, func() bool {
					return false
				})
			}
		}, "child")
		c.engine.Subscribe(child)
	case actor.ActorUnhealthyEvent:
		// Handle unhealthy actor
	}
}, "parent")
```
