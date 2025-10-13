package actor

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/DataDog/gostackparse"
)

type Envelope struct {
	Msg    any
	Sender *PID
}

// Processer abstracts process behavior.
type Processer interface {
	Start()
	PID() *PID
	Send(*PID, any, *PID)
	Invoke([]Envelope)
	Shutdown()
}

// process represents an actor process.
type process struct {
	Opts

	inbox    Inboxer
	context  *Context
	pid      *PID
	restarts int32
	mbuffer  []Envelope
	mcount   int32
}

// newProcess creates a new process.
func newProcess(e *Engine, opts Opts) *process {
	pid := NewPID(e.address, opts.Kind+pidSeparator+opts.ID)
	ctx := newContext(opts.Context, e, pid)
	p := &process{
		pid:     pid,
		inbox:   NewInbox(opts.InboxSize),
		Opts:    opts,
		context: ctx,
		mbuffer: nil,
	}
	ctx.getInboxCount = p.Count // Set after p is created
	return p
}

// applyMiddleware applies middleware in reverse order.
func applyMiddleware(rcv ReceiveFunc, middleware ...MiddlewareFunc) ReceiveFunc {
	for i := len(middleware) - 1; i >= 0; i-- {
		rcv = middleware[i](rcv)
	}
	return rcv
}

// Invoke processes a batch of messages.
func (p *process) Invoke(msgs []Envelope) {
	nmsg := len(msgs)
	nproc := 0
	processed := 0
	atomic.StoreInt32(&p.mcount, int32(nmsg))
	defer func() {
		if v := recover(); v != nil {
			p.context.message = Stopped{}
			p.context.receiver.Receive(p.context)

			p.mbuffer = make([]Envelope, nmsg-nproc)
			copy(p.mbuffer, msgs[nproc:nmsg])
			atomic.StoreInt32(&p.mcount, int32(nmsg-nproc))
			p.tryRestart(v)
		}
	}()
	for i := 0; i < nmsg; i++ {
		nproc++
		atomic.AddInt32(&p.mcount, -1)
		msg := msgs[i]
		if pill, ok := msg.Msg.(poisonPill); ok {
			if pill.graceful {
				for _, m := range msgs[processed:] {
					p.invokeMsg(m)
				}
			}
			p.cleanup(pill.cancel)
			return
		}
		p.invokeMsg(msg)
		processed++
	}
}

// invokeMsg processes a single Envelope.
func (p *process) invokeMsg(msg Envelope) {
	if _, ok := msg.Msg.(poisonPill); ok {
		return
	}
	p.context.message = msg.Msg
	p.context.sender = msg.Sender
	recv := p.context.receiver
	if len(p.Opts.Middleware) > 0 {
		applyMiddleware(recv.Receive, p.Opts.Middleware...)(p.context)
	} else {
		recv.Receive(p.context)
	}
}

// Start initializes and runs the process.
func (p *process) Start() {
	recv := p.Producer()
	p.context.receiver = recv
	defer func() {
		if v := recover(); v != nil {
			p.context.message = Stopped{}
			p.context.receiver.Receive(p.context)
			p.tryRestart(v)
		}
	}()
	p.context.message = Initialized{}
	applyMiddleware(recv.Receive, p.Opts.Middleware...)(p.context)
	p.context.engine.BroadcastEvent(ActorInitializedEvent{PID: p.pid, Timestamp: time.Now()})

	p.context.message = Started{}
	applyMiddleware(recv.Receive, p.Opts.Middleware...)(p.context)
	p.context.engine.BroadcastEvent(ActorStartedEvent{PID: p.pid, Timestamp: time.Now()})

	if len(p.mbuffer) > 0 {
		p.Invoke(p.mbuffer)
		p.mbuffer = nil
	}

	p.inbox.Start(p)
}

// tryRestart handles process restarts and max restarts logic.
func (p *process) tryRestart(v any) {
	if msg, ok := v.(*InternalError); ok {
		slog.Error(msg.From, "err", msg.Err)
		time.Sleep(p.Opts.RestartDelay)
		p.Start()
		return
	}
	stackTrace := cleanTrace(debug.Stack())
	if p.restarts == p.MaxRestarts {
		p.context.engine.BroadcastEvent(ActorMaxRestartsExceededEvent{
			PID:       p.pid,
			Timestamp: time.Now(),
		})
		p.cleanup(nil)
		return
	}
	p.context.message = Stopped{}
	p.context.receiver.Receive(p.context)

	p.restarts++
	p.context.engine.BroadcastEvent(ActorRestartedEvent{
		PID:        p.pid,
		Timestamp:  time.Now(),
		Stacktrace: stackTrace,
		Reason:     v,
		Restarts:   p.restarts,
	})
	time.Sleep(p.Opts.RestartDelay)
	p.Start()
}

// cleanup stops process, removes from registry, notifies children.
func (p *process) cleanup(cancel context.CancelFunc) {
	if cancel != nil {
		defer cancel()
	}
	if p.context.parentCtx != nil {
		p.context.parentCtx.children.Delete(p.pid.ID)
	}
	if p.context.children.Len() > 0 {
		children := p.context.Children()
		for _, pid := range children {
			<-p.context.engine.Poison(pid).Done()
		}
	}
	p.inbox.Stop()
	p.context.engine.Registry.Remove(p.pid)
	p.context.message = Stopped{}
	applyMiddleware(p.context.receiver.Receive, p.Opts.Middleware...)(p.context)
	p.context.engine.BroadcastEvent(ActorStoppedEvent{PID: p.pid, Timestamp: time.Now()})
}

func (p *process) PID() *PID { return p.pid }

// Send places a message in the process's inbox.
func (p *process) Send(_ *PID, msg any, sender *PID) {
	p.inbox.Send(Envelope{Msg: msg, Sender: sender})
}

// Shutdown gracefully stops the process.
func (p *process) Shutdown() {
	p.cleanup(nil)
}

// Count returns the number of messages in the inbox and buffer.
func (p *process) Count() int {
	return p.inbox.Count() + int(atomic.LoadInt32(&p.mcount))
}

// cleanTrace formats stack trace for logging.
func cleanTrace(stack []byte) []byte {
	goros, err := gostackparse.Parse(bytes.NewReader(stack))
	if err != nil {
		slog.Error("failed to parse stacktrace", "err", err)
		return stack
	}
	if len(goros) != 1 {
		slog.Error("expected only one goroutine", "goroutines", len(goros))
		return stack
	}
	// skip the first frames:
	if len(goros[0].Stack) > 4 {
		goros[0].Stack = goros[0].Stack[4:]
	}
	buf := bytes.NewBuffer(nil)
	_, _ = fmt.Fprintf(buf, "goroutine %d [%s]\n", goros[0].ID, goros[0].State)
	for _, frame := range goros[0].Stack {
		_, _ = fmt.Fprintf(buf, "%s\n", frame.Func)
		_, _ = fmt.Fprint(buf, "\t", frame.File, ":", frame.Line, "\n")
	}
	return buf.Bytes()
}
