package steerloop

import (
	"context"
	"errors"
	"sync"

	"github.com/cloudwego/eino/schema"
)

// ErrNoActiveLoop is returned by a Registry when the key has no running loop.
var ErrNoActiveLoop = errors.New("steerloop: no active loop")

// ActiveLoop is a running Loop with the function that cancels its context.
type ActiveLoop struct {
	Loop   *Loop
	Cancel context.CancelFunc
}

// Registry tracks running loops by a key of the caller's choice, such as a
// user or session identifier, so another goroutine can stop or steer them.
// The zero value is not usable; call NewRegistry.
type Registry[K comparable] struct {
	mu    sync.RWMutex
	loops map[K]*ActiveLoop
}

// NewRegistry returns an empty registry.
func NewRegistry[K comparable]() *Registry[K] {
	return &Registry[K]{loops: make(map[K]*ActiveLoop)}
}

// Register records loop under key, replacing any loop already there.
func (r *Registry[K]) Register(key K, loop *Loop, cancel context.CancelFunc) {
	if r == nil || loop == nil {
		return
	}
	r.mu.Lock()
	r.loops[key] = &ActiveLoop{Loop: loop, Cancel: cancel}
	r.mu.Unlock()
}

// Unregister removes the loop under key.
func (r *Registry[K]) Unregister(key K) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.loops, key)
	r.mu.Unlock()
}

// Get returns the loop under key.
func (r *Registry[K]) Get(key K) (*ActiveLoop, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	al, ok := r.loops[key]
	r.mu.RUnlock()
	if !ok || al == nil || al.Loop == nil {
		return nil, false
	}
	return al, true
}

// Has reports whether key has a registered loop.
func (r *Registry[K]) Has(key K) bool {
	_, ok := r.Get(key)
	return ok
}

// Stop stops the loop under key. It returns ErrNoActiveLoop when there is none.
func (r *Registry[K]) Stop(key K) error {
	al, ok := r.Get(key)
	if !ok {
		return ErrNoActiveLoop
	}
	al.Loop.Stop()
	return nil
}

// Inject sends a user message to the loop under key. It returns
// ErrNoActiveLoop when there is none and ErrControlQueueFull when the loop's
// control queue is full.
func (r *Registry[K]) Inject(key K, message string) error {
	al, ok := r.Get(key)
	if !ok {
		return ErrNoActiveLoop
	}
	if !al.Loop.Inject(message) {
		return ErrControlQueueFull
	}
	return nil
}

// RunningMessages returns a copy of the conversation of the loop under key.
func (r *Registry[K]) RunningMessages(key K) ([]*schema.Message, error) {
	al, ok := r.Get(key)
	if !ok {
		return nil, ErrNoActiveLoop
	}
	return al.Loop.GetRunningMessages(), nil
}
