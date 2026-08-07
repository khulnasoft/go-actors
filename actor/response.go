package actor

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"
)

// responseSeq provides unique, monotonically increasing response mailbox IDs
// without paying for a full-range rand match on every request.
var responseSeq atomic.Uint64

// Response is the mailbox for a single in-flight request/response exchange.
// A request is routed to the Response via its short-lived PID; the result is
// delivered on a buffered channel and read by Response.Result().
type Response struct {
	engine  *Engine
	pid     *PID
	result  chan any
	timeout time.Duration
}

func NewResponse(e *Engine, timeout time.Duration) *Response {
	return &Response{
		engine:  e,
		result:  make(chan any, 1),
		timeout: timeout,
		pid:     NewPID(e.address, responsePrefix+pidSeparator+strconv.FormatUint(responseSeq.Add(1), 36)),
	}
}

func (r *Response) Result() (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer func() {
		cancel()
		r.engine.Registry.Remove(r.pid)
	}()

	select {
	case resp := <-r.result:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Response) Send(_ *PID, msg any, _ *PID) {
	r.result <- msg
}

func (r *Response) PID() *PID         { return r.pid }
func (r *Response) Shutdown()         {}
func (r *Response) Start()            {}
func (r *Response) Invoke([]Envelope) {}