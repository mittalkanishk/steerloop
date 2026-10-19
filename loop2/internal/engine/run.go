// Package engine is the loop itself. Start at Run.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/loop2/internal/audit"
	"github.com/mittalkanishk/steerloop/loop2/internal/control"
	"github.com/mittalkanishk/steerloop/loop2/internal/emitter"
	"github.com/mittalkanishk/steerloop/loop2/internal/state"
	"github.com/mittalkanishk/steerloop/loop2/internal/transcript"
)

// ModelCaller makes one logical model call. → internal/modelcall
type ModelCaller interface {
	Call(ctx context.Context, m model.ToolCallingChatModel, msgs []*schema.Message) (*schema.Message, error)
}

// ToolDispatcher runs one tool call; false means the run ended. → internal/dispatch
type ToolDispatcher interface {
	Dispatch(ctx context.Context, tc schema.ToolCall) bool
}

// StopPolicy is the run's stop condition, chosen once in wire.go. → internal/stopping
type StopPolicy interface {
	Label() string
	// FinishCall returns the call that ends the run, when the reply has one.
	FinishCall(reply *schema.Message) (schema.ToolCall, bool)
	// AfterText says whether a text-only reply ends the run, and what
	// message, if any, to send the model when it does not.
	AfterText() (ends bool, nudge string)
}

// Inbox holds controls sent to the run. → internal/control
type Inbox interface {
	Drain(n control.Notifier) bool
	StopRequested() bool
}

// HistoryCompactor shapes the history sent to the model; the stored history
// is not changed. → the caller's loop2.HistoryCompactor, or defaults.FullHistory
type HistoryCompactor interface {
	Compact(msgs []*schema.Message) []*schema.Message
}

// Deps is everything an Engine works with. wire.go fills it. State,
// emitter, audit and transcript are concrete: each has one implementation.
type Deps struct {
	Model      model.ToolCallingChatModel
	Caller     ModelCaller
	Tools      ToolDispatcher
	Stop       StopPolicy
	Inbox      Inbox
	Compactor  HistoryCompactor
	State      *state.Run
	Emitter    *emitter.Emitter
	Finish     *Finisher
	Audit      *audit.Recorder
	Transcript *transcript.Logger
	Logger     *slog.Logger

	TotalTimeout     time.Duration
	ReflectionPrompt string
}

type Engine struct {
	d Deps
}

func New(d Deps) *Engine {
	return &Engine{d: d}
}

// Run is the loop. loop2.Loop.Start runs it as its own goroutine (1). Each
// model call may add two short-lived goroutines inside modelcall (2: the
// stream reader, 3: the step-timeout watcher); both end when that call
// returns. Run always ends through Finisher.Close, which closes the event
// channel.
func (e *Engine) Run(ctx context.Context) {
	defer e.d.Finish.Close()

	ctx, cancel := e.runContext(ctx)
	defer cancel()

	for {
		if reason, over := e.limitReached(ctx); over {
			e.d.Finish.Done(reason, "")
			return
		}
		if e.d.Inbox.Drain(e) || e.d.Inbox.StopRequested() {
			e.d.Finish.EndStopped()
			return
		}

		t, ok := e.callModel(ctx)
		if !ok {
			return
		}

		var keepGoing bool
		if len(t.reply.ToolCalls) > 0 {
			keepGoing = e.handleToolCalls(ctx, t)
		} else {
			keepGoing = e.handleText(ctx, t)
		}
		if !keepGoing {
			return
		}
	}
}

func (e *Engine) runContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if e.d.TotalTimeout > 0 {
		return context.WithTimeout(ctx, e.d.TotalTimeout)
	}
	return context.WithCancel(ctx)
}

func (e *Engine) limitReached(ctx context.Context) (event.DoneReason, bool) {
	switch {
	case e.d.State.Iteration() >= e.d.State.MaxIterations():
		return event.ReasonMaxIterations, true
	case ctx.Err() != nil:
		return event.ReasonTimeout, true
	default:
		return "", false
	}
}

// handleToolCalls runs every tool the reply asks for. It returns false when
// the run ended.
func (e *Engine) handleToolCalls(ctx context.Context, t turn) bool {
	if call, ok := e.d.Stop.FinishCall(t.reply); ok {
		e.d.Emitter.Emit(event.ToolCall, event.ToolCallData{ToolCallID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
		e.d.Finish.Done(event.ReasonFinished, call.Function.Arguments)
		return false
	}

	e.d.State.Append(t.reply)
	e.recordTurn(t)

	ended := false
	for _, tc := range t.reply.ToolCalls {
		if !e.d.Tools.Dispatch(ctx, tc) {
			ended = true
			break
		}
	}
	e.logTranscript(ctx, t.index)
	return !ended
}

// handleText handles a reply with no tool call. It returns false when the
// run ended.
func (e *Engine) handleText(ctx context.Context, t turn) bool {
	content := t.reply.Content
	e.d.Emitter.Emit(event.Response, event.ResponseData{Content: content})
	e.d.State.Append(t.reply)

	ends, nudge := e.d.Stop.AfterText()
	if ends {
		if e.d.State.ReflectionEnabled() {
			e.reflect(ctx, content)
		}
		e.logTranscript(ctx, t.index)
		e.d.Finish.Done(event.ReasonFinished, content)
		return false
	}

	if nudge != "" {
		e.d.State.Append(&schema.Message{Role: schema.User, Content: nudge})
		e.d.Logger.Debug("agent loop auto-nudged after text-only response", slog.Int("iteration", t.index))
	}
	e.logTranscript(ctx, t.index)
	return true
}

func (e *Engine) logTranscript(ctx context.Context, iteration int) {
	start, added := e.d.State.TakeUnlogged()
	e.d.Transcript.Diff(ctx, iteration, start, added)
}

func thinkingMessage(iteration, max int) string {
	return fmt.Sprintf("Reasoning (step %d/%d)…", iteration, max)
}
