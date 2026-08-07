package actor

import (
	"sync"
	"sync/atomic"
)

const LocalLookupAddr = "local"

// registrySnapshot is an immutable copy-on-write map of registered processes.
// Readers load the current snapshot pointer and read from it lock-free;
// writers clone it under a mutex, mutate the copy, and republish it.
type registrySnapshot map[string]Processer

// Registry maps actor PIDs to their Processer.
//
// The long-lived actor table is copy-on-write: reads (the hot send path) are
// lock-free atomic loads; writes (spawn/stop) clone the immutable snapshot
// under a short-lived mutex. Short-lived request/response entries live in a
// dedicated, highly-churned table so they never force a snapshot copy.
type Registry struct {
	mu        sync.Mutex
	lookup    atomic.Pointer[registrySnapshot]
	responses map[string]Processer
	engine    *Engine
}

func newRegistry(e *Engine) *Registry {
	snap := registrySnapshot(make(map[string]Processer, 1024))
	r := &Registry{
		responses: make(map[string]Processer),
		engine:    e,
	}
	r.lookup.Store(&snap)
	return r
}

// GetPID returns the process id associated for the given kind and its id.
// GetPID returns nil if the process was not found.
func (r *Registry) GetPID(kind, id string) *PID {
	proc := r.getByID(kind + pidSeparator + id)
	if proc != nil {
		return proc.PID()
	}
	return nil
}

// Remove removes the given PID from the registry.
func (r *Registry) Remove(pid *PID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	snap := *r.lookup.Load()
	// Fast path: if the PID is a short-lived response, remove it there.
	if pid.isResponse() {
		if _, ok := r.responses[pid.ID]; ok {
			delete(r.responses, pid.ID)
			return
		}
	}
	next := make(registrySnapshot, len(snap))
	for k, v := range snap {
		if k != pid.ID {
			next[k] = v
		}
	}
	r.lookup.Store(&next)
}

// get returns the processer for the given PID, if it exists.
// If it doesn't exist, nil is returned so the caller must check for that
// and direct the message to the deadletter processer instead.
func (r *Registry) get(pid *PID) Processer {
	if pid == nil {
		return nil
	}
	snap := r.lookup.Load()
	if proc, ok := (*snap)[pid.ID]; ok {
		return proc
	}
	if pid.isResponse() {
		// short-lived requests are stored in the response table.
		r.mu.Lock()
		proc, ok := r.responses[pid.ID]
		r.mu.Unlock()
		if ok {
			return proc
		}
	}
	return nil // didn't find the processer
}

func (r *Registry) getByID(id string) Processer {
	snap := r.lookup.Load()
	return (*snap)[id]
}

func (r *Registry) add(proc Processer) {
	r.mu.Lock()
	id := proc.PID().ID
	snap := *r.lookup.Load()
	if _, ok := snap[id]; ok {
		r.mu.Unlock()
		r.engine.BroadcastEvent(ActorDuplicateIdEvent{PID: proc.PID()})
		return
	}
	clone := make(registrySnapshot, len(snap)+1)
	for k, v := range snap {
		clone[k] = v
	}
	clone[id] = proc
	r.lookup.Store(&clone)
	r.mu.Unlock()
	proc.Start()
}

// registerResponse registers a pending request so that its reply can be
// routed to the response mailbox without touching the copy-on-write snapshot.
func (r *Registry) registerResponse(proc Processer) {
	r.mu.Lock()
	r.responses[proc.PID().ID] = proc
	r.mu.Unlock()
}