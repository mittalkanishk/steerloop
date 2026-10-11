package steerlooptest

import (
	"context"
	"fmt"
	"sync"
)

// ExecutedCall is one tool call seen by a RecordingExecutor.
type ExecutedCall struct {
	Name string
	Args string
}

// RecordingExecutor is a tool executor that records every call and answers
// with a handler. It satisfies steerloop.ToolExecutor.
type RecordingExecutor struct {
	mu      sync.Mutex
	calls   []ExecutedCall
	handler func(ctx context.Context, name, args string) (string, error)
}

// NewRecordingExecutor returns an executor that answers with handler. A nil
// handler answers "result from <tool>".
func NewRecordingExecutor(handler func(ctx context.Context, name, args string) (string, error)) *RecordingExecutor {
	if handler == nil {
		handler = func(_ context.Context, name, _ string) (string, error) {
			return fmt.Sprintf("result from %s", name), nil
		}
	}
	return &RecordingExecutor{handler: handler}
}

// ExecuteTool records the call and runs the handler.
func (e *RecordingExecutor) ExecuteTool(ctx context.Context, name string, arguments string) (string, error) {
	e.mu.Lock()
	e.calls = append(e.calls, ExecutedCall{Name: name, Args: arguments})
	e.mu.Unlock()
	return e.handler(ctx, name, arguments)
}

// CallCount returns the number of tool calls made.
func (e *RecordingExecutor) CallCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

// Calls returns every tool call, in order.
func (e *RecordingExecutor) Calls() []ExecutedCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]ExecutedCall, len(e.calls))
	copy(out, e.calls)
	return out
}

// CalledTools returns the names of the tools called, in order.
func (e *RecordingExecutor) CalledTools() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	names := make([]string, len(e.calls))
	for i, c := range e.calls {
		names[i] = c.Name
	}
	return names
}
