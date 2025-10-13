package actor

import (
	"context"
	"log/slog"
	"math"
	"math/rand"
	"strconv"
	"time"

	"github.com/khulnasoft/goactors/safemap"
)

type Context struct {
	pid          *PID
	sender       *PID
	engine       *Engine
	receiver     Receiver
	message      any
	getInboxCount func() int
	parentCtx    *Context
	children     *safemap.SafeMap[string, *PID]
	context      context.Context
}

func newContext(ctx context.Context, e *Engine, pid *PID) *Context {
	return &Context{
		context:      ctx,
		engine:       e,
		pid:          pid,
		children:     safemap.New[string, *PID](),
		getInboxCount: func() int { return -1 },
	}
}

func (c *Context) Context() context.Context {
	return c.context
}

func (c *Context) Receiver() Receiver {
	return c.receiver
}

func (c *Context) Request(pid *PID, msg any, timeout time.Duration) *Response {
	return c.engine.Request(pid, msg, timeout)
}

func (c *Context) Respond(msg any) {
	if c.sender == nil {
		slog.Warn("context got no sender", "func", "Respond", "pid", c.PID())
		return
	}
	c.engine.Send(c.sender, msg)
}

func (c *Context) SpawnChild(p Producer, name string, opts ...OptFunc) *PID {
	options := DefaultOpts(p)
	options.Kind = c.PID().ID + pidSeparator + name
	for _, opt := range opts {
		opt(&options)
	}
	if len(options.ID) == 0 {
		id := strconv.Itoa(rand.Intn(math.MaxInt))
		options.ID = id
	}
	proc := newProcess(c.engine, options)
	proc.context.parentCtx = c
	pid := c.engine.SpawnProc(proc)
	c.children.Set(pid.ID, pid)
	return proc.PID()
}

func (c *Context) SpawnChildFunc(f func(*Context), name string, opts ...OptFunc) *PID {
	return c.SpawnChild(newFuncReceiver(f), name, opts...)
}

func (c *Context) Send(pid *PID, msg any) {
	c.engine.SendWithSender(pid, msg, c.pid)
}

func (c *Context) SendRepeat(pid *PID, msg any, interval time.Duration) SendRepeater {
	sr := SendRepeater{
		engine:   c.engine,
		self:     c.pid,
		target:   pid.CloneVT(),
		interval: interval,
		msg:      msg,
		cancelch: make(chan struct{}, 1),
	}
	sr.start()
	return sr
}

func (c *Context) Forward(pid *PID) {
	c.engine.SendWithSender(pid, c.message, c.pid)
}

func (c *Context) GetPID(id string) *PID {
	proc := c.engine.Registry.getByID(id)
	if proc != nil {
		return proc.PID()
	}
	return nil
}

func (c *Context) Parent() *PID {
	if c.parentCtx != nil {
		return c.parentCtx.pid
	}
	return nil
}

func (c *Context) Child(id string) *PID {
	pid, _ := c.children.Get(id)
	return pid
}

func (c *Context) Children() []*PID {
	pids := make([]*PID, c.children.Len())
	i := 0
	c.children.ForEach(func(_ string, child *PID) {
		pids[i] = child
		i++
	})
	return pids
}

func (c *Context) PID() *PID {
	return c.pid
}

func (c *Context) Sender() *PID {
	return c.sender
}

func (c *Context) Engine() *Engine {
	return c.engine
}

func (c *Context) Message() any {
	return c.message
}

func (c *Context) GetInboxCount() int {
	return c.getInboxCount()
}
