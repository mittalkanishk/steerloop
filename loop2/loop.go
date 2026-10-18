// Package loop2 is a controllable agent loop for CloudWeGo Eino, in an
// interface-first, nested shape. Read in this order:
//
//	loop.go                     the public Loop: what a caller can do
//	wire.go                     which concrete type fills each role
//	internal/engine/run.go      the loop itself, and every role it needs
//	then any internal package wire.go names
package loop2

import (
	"context"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/loop2/internal/control"
	"github.com/mittalkanishk/steerloop/loop2/internal/emitter"
	"github.com/mittalkanishk/steerloop/loop2/internal/engine"
	"github.com/mittalkanishk/steerloop/loop2/internal/state"
	"github.com/mittalkanishk/steerloop/loop2/internal/transcript"
)

// ToolExecutor runs the tools the model asks for. An error is not fatal: the
// model receives it as the tool's result and can react.
type ToolExecutor interface {
	ExecuteTool(ctx context.Context, name, arguments string) (string, error)
}

// ConfigChange changes settings while the loop runs. Nil fields are left
// unchanged.
type ConfigChange = state.Change

// State is the lifecycle state of a Loop.
type State = state.Status

const (
	StateIdle    = state.Idle
	StateRunning = state.Running
	StateDone    = state.Done
	StateStopped = state.Stopped
	StateError   = state.Error
)

// Loop runs one agent conversation: call the model, run the tools it asks
// for, feed the results back, repeat. Start it once; it is safe for
// concurrent use. New (wire.go) builds it.
type Loop struct {
	engine     *engine.Engine
	finish     *engine.Finisher
	state      *state.Run
	emitter    *emitter.Emitter
	inbox      *control.Inbox
	transcript *transcript.Logger
}

// Start begins the run and returns the event channel, which is closed after
// the done event. The loop keeps its own copy of messages. A second call
// returns a channel holding one fatal error.
func (l *Loop) Start(ctx context.Context, messages []*schema.Message) <-chan event.Event {
	if !l.state.Begin(messages) {
		return failedStart("loop already started")
	}
	l.transcript.Seed(ctx, messages)
	go l.engine.Run(ctx)
	return l.emitter.Events()
}

// Stop asks the loop to stop at the next iteration boundary, or at once if a
// pause handler is waiting for a reply. It cannot be lost to a full queue.
func (l *Loop) Stop() { l.inbox.Stop() }

// Inject queues a user message. It returns false when the queue is full.
func (l *Loop) Inject(message string) bool {
	return l.inbox.Send(control.Control{Kind: control.Inject, Message: message})
}

// ModifyConfig queues a settings change. It returns false when the queue is full.
func (l *Loop) ModifyConfig(c ConfigChange) bool {
	return l.inbox.Send(control.Control{Kind: control.ModifyConfig, Change: c})
}

// Result returns the outcome. ok is false until the loop has ended.
func (l *Loop) Result() (event.DoneData, bool) { return l.state.Result() }

// Wait blocks until the run has ended and the event channel is closed.
func (l *Loop) Wait() { l.finish.Wait() }

// Events returns the event channel, the same one Start returns.
func (l *Loop) Events() <-chan event.Event { return l.emitter.Events() }

// Iteration returns the number of model calls started so far.
func (l *Loop) Iteration() int { return l.state.Iteration() }

// Messages returns a copy of the conversation as it stands now.
func (l *Loop) Messages() []*schema.Message { return l.state.Messages() }

// State returns the lifecycle state.
func (l *Loop) State() State { return l.state.Status() }

func failedStart(msg string) <-chan event.Event {
	ch := make(chan event.Event, 1)
	ch <- event.Event{Type: event.Error, Data: event.ErrorData{Error: msg, Fatal: true}, Timestamp: time.Now().UTC()}
	close(ch)
	return ch
}
