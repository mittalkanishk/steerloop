// Package state holds all mutable state of one loop run behind one mutex.
// Nothing else in loop2 keeps shared state, so this is the only place to
// look for data races.
package state

import (
	"slices"
	"sync"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/event"
)

// Status is the lifecycle state of a run.
type Status string

const (
	// Idle is the state before Start.
	Idle Status = "idle"
	// Running is the state between Start and the done event.
	Running Status = "running"
	// Done means the loop ended on its own: finished, max iterations,
	// timeout, or handed over to a human.
	Done Status = "done"
	// Stopped means the loop ended because it was told to stop.
	Stopped Status = "stopped"
	// Error means the loop ended on a fatal error.
	Error Status = "error"
)

// Run is one run's state. settings.go and usage.go hold the rest of its
// methods.
type Run struct {
	mu        sync.RWMutex
	status    Status
	iteration int
	messages  []*schema.Message
	logged    int // messages already written to the transcript log
	settings  Settings
	usage     Usage
	lastError string
	result    event.DoneData
	hasResult bool
}

func New(settings Settings) *Run {
	return &Run{status: Idle, settings: settings}
}

// Begin moves Idle to Running with a copy of the starting messages. It
// returns false if the run had already started.
func (r *Run) Begin(msgs []*schema.Message) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status != Idle {
		return false
	}
	r.status = Running
	r.messages = make([]*schema.Message, len(msgs))
	copy(r.messages, msgs)
	r.logged = len(msgs)
	return true
}

func (r *Run) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *Run) SetStatus(s Status) {
	r.mu.Lock()
	r.status = s
	r.mu.Unlock()
}

// MarkDoneIfRunning settles a run that ended without a more specific status.
func (r *Run) MarkDoneIfRunning() {
	r.mu.Lock()
	if r.status == Running {
		r.status = Done
	}
	r.mu.Unlock()
}

// Fail records a fatal error.
func (r *Run) Fail(msg string) {
	r.mu.Lock()
	r.status = Error
	r.lastError = msg
	r.mu.Unlock()
}

func (r *Run) LastError() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastError
}

// NextIteration counts one more model call and returns its number.
func (r *Run) NextIteration() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.iteration++
	return r.iteration
}

// Iteration is the number of model calls started so far.
func (r *Run) Iteration() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.iteration
}

// Messages returns a copy of the conversation as it stands now.
func (r *Run) Messages() []*schema.Message {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*schema.Message, len(r.messages))
	copy(out, r.messages)
	return out
}

// LastReply returns the text of the model's latest reply that had any, or
// "" when there is none.
func (r *Run) LastReply() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, m := range slices.Backward(r.messages) {
		if m.Role == schema.Assistant && m.Content != "" {
			return m.Content
		}
	}
	return ""
}

func (r *Run) Append(msg *schema.Message) {
	r.mu.Lock()
	r.messages = append(r.messages, msg)
	r.mu.Unlock()
}

// TakeUnlogged returns the messages added since the last call, and the index
// of the first one.
func (r *Run) TakeUnlogged() (start int, added []*schema.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	start = r.logged
	if start > len(r.messages) {
		start = 0
	}
	added = make([]*schema.Message, len(r.messages)-start)
	copy(added, r.messages[start:])
	r.logged = len(r.messages)
	return start, added
}

// SetResult records the run's outcome.
func (r *Run) SetResult(d event.DoneData) {
	r.mu.Lock()
	r.result = d
	r.hasResult = true
	r.mu.Unlock()
}

// Result returns the outcome. ok is false until the run has ended.
func (r *Run) Result() (event.DoneData, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.result, r.hasResult
}
