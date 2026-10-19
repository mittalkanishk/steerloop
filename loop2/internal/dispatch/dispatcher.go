// Package dispatch runs one tool call: through the ToolExecutor, or through
// the pause handler registered for that tool.
package dispatch

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/loop2/pause"
)

// Executor runs a tool. → the caller's loop2.ToolExecutor
type Executor interface {
	ExecuteTool(ctx context.Context, name, arguments string) (string, error)
}

// Emitter sends events. → emitter.Emitter
type Emitter interface {
	Emit(t event.Type, data any)
}

// History is the conversation. → state.Run
type History interface {
	Append(msg *schema.Message)
	Messages() []*schema.Message
}

// ModelCaller makes one model call for a pause handler. → modelcall.Caller
type ModelCaller interface {
	Call(ctx context.Context, m model.ToolCallingChatModel, msgs []*schema.Message) (*schema.Message, error)
}

// ReplyWaiter waits for the next injected message. → control.Inbox
type ReplyWaiter interface {
	WaitInject(ctx context.Context) (string, bool)
}

// Ender ends the run when a pause handler says to. → engine.Finisher
type Ender interface {
	EndAwaitingHuman(awaiting event.AwaitingInputData)
	EndStopped()
}

// Deps is everything a Dispatcher works with. wire.go fills it.
type Deps struct {
	Executor Executor
	Handlers map[string]pause.Handler // tool name → handler, in place of Executor
	Emitter  Emitter
	History  History
	Caller   ModelCaller
	Replies  ReplyWaiter
	Ender    Ender
	Model    model.ToolCallingChatModel
	Tools    []*schema.ToolInfo
	Logger   *slog.Logger
}

type Dispatcher struct {
	d Deps
}

func New(d Deps) *Dispatcher {
	return &Dispatcher{d: d}
}

// Dispatch runs one tool call and records its result in the history. It
// returns false when a pause handler ended the run.
func (x *Dispatcher) Dispatch(ctx context.Context, tc schema.ToolCall) bool {
	x.d.Emitter.Emit(event.ToolCall, event.ToolCallData{ToolCallID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})

	if h, ok := x.d.Handlers[tc.Function.Name]; ok {
		return x.runHandler(ctx, h, tc)
	}
	x.runTool(ctx, tc)
	return true
}

func (x *Dispatcher) runTool(ctx context.Context, tc schema.ToolCall) {
	start := time.Now()
	result, err := x.execute(ctx, tc.Function.Name, tc.Function.Arguments)

	errText := ""
	if err != nil {
		errText = err.Error()
		result = fmt.Sprintf("Error executing tool %q: %s", tc.Function.Name, errText)
		x.d.Logger.Warn("agent loop tool execution error", slog.String("tool", tc.Function.Name), slog.String("error", errText))
	}
	x.recordResult(tc, result, errText, time.Since(start))
}

// runHandler hands the call to its pause handler and waits for it.
func (x *Dispatcher) runHandler(ctx context.Context, h pause.Handler, tc schema.ToolCall) bool {
	start := time.Now()
	s := &session{x: x}
	call := pause.Call{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments}

	reply, keepGoing := x.handle(ctx, h, call, s)
	if !keepGoing {
		switch {
		case s.awaiting != nil:
			x.d.Ender.EndAwaitingHuman(*s.awaiting)
		case reply == "":
			x.d.Ender.EndStopped()
		}
		return false
	}

	if reply == "" {
		reply = "(no response)"
	}
	x.recordResult(tc, reply, "", time.Since(start))
	return true
}

func (x *Dispatcher) recordResult(tc schema.ToolCall, result, errText string, took time.Duration) {
	x.d.Emitter.Emit(event.ToolResult, event.ToolResultData{
		ToolCallID: tc.ID,
		Name:       tc.Function.Name,
		Result:     result,
		Error:      errText,
		DurationMs: took.Milliseconds(),
	})
	x.d.History.Append(&schema.Message{Role: schema.Tool, Content: result, ToolCallID: tc.ID, ToolName: tc.Function.Name})
}

// execute runs the executor and turns a panic into a tool error, so one
// faulty tool cannot take the process down.
func (x *Dispatcher) execute(ctx context.Context, name, args string) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool panicked: %v", r)
			x.d.Emitter.Emit(event.Error, event.ErrorData{Error: fmt.Sprintf("tool %q panicked: %v", name, r)})
		}
	}()
	return x.d.Executor.ExecuteTool(ctx, name, args)
}

// handle calls the handler. A panicking handler is treated like a failed tool.
func (x *Dispatcher) handle(ctx context.Context, h pause.Handler, call pause.Call, s *session) (reply string, keepGoing bool) {
	defer func() {
		if r := recover(); r != nil {
			x.d.Emitter.Emit(event.Error, event.ErrorData{Error: fmt.Sprintf("pause %q panicked: %v", call.Name, r)})
			reply = fmt.Sprintf("Error executing tool %q: pause function panicked: %v", call.Name, r)
			keepGoing = true
		}
	}()
	return h.Handle(ctx, call, s)
}
