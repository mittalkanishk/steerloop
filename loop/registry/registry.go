// Package registry tracks running loops by a key of the caller's choice, such
// as a user or session id, so another goroutine can stop or steer them.
package registry

import (
	"context"
	"errors"
	"sync"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop"
)

var (
	// ErrNoActiveLoop: the key has no running loop.
	ErrNoActiveLoop = errors.New("loop: no active loop")
	// ErrControlQueueFull: the loop's control queue is full.
	ErrControlQueueFull = errors.New("loop: control queue full")
)

// Active is a running Loop with the function that cancels its context.
type Active struct {
	Loop   *loop.Loop
	Cancel context.CancelFunc
}

// Registry is safe for concurrent use. The zero value is not usable; call New.
type Registry[K comparable] struct {
	mu    sync.RWMutex
	loops map[K]*Active
}

func New[K comparable]() *Registry[K] {
	return &Registry[K]{loops: make(map[K]*Active)}
}

// Register records loop under key, replacing any loop already there.
func (r *Registry[K]) Register(key K, loop *loop.Loop, cancel context.CancelFunc) {
	if loop == nil {
		return
	}
	r.mu.Lock()
	r.loops[key] = &Active{Loop: loop, Cancel: cancel}
	r.mu.Unlock()
}

func (r *Registry[K]) Unregister(key K) {
	r.mu.Lock()
	delete(r.loops, key)
	r.mu.Unlock()
}

func (r *Registry[K]) Get(key K) (*Active, bool) {
	r.mu.RLock()
	a, ok := r.loops[key]
	r.mu.RUnlock()
	return a, ok && a != nil && a.Loop != nil
}

func (r *Registry[K]) Has(key K) bool {
	_, ok := r.Get(key)
	return ok
}

func (r *Registry[K]) Stop(key K) error {
	a, ok := r.Get(key)
	if !ok {
		return ErrNoActiveLoop
	}
	a.Loop.Stop()
	return nil
}

func (r *Registry[K]) Inject(key K, message string) error {
	a, ok := r.Get(key)
	if !ok {
		return ErrNoActiveLoop
	}
	if !a.Loop.Inject(message) {
		return ErrControlQueueFull
	}
	return nil
}

func (r *Registry[K]) Messages(key K) ([]*schema.Message, error) {
	a, ok := r.Get(key)
	if !ok {
		return nil, ErrNoActiveLoop
	}
	return a.Loop.Messages(), nil
}
