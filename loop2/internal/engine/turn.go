package engine

import (
	"context"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/loop2/internal/audit"
	"github.com/mittalkanishk/steerloop/loop2/internal/state"
)

// turn is one model call and what it took.
type turn struct {
	index       int
	request     []*schema.Message
	reply       *schema.Message
	usageBefore state.Usage
	took        time.Duration
}

// callModel starts the next iteration and makes its model call. It returns
// false when the call failed, which ends the run.
func (e *Engine) callModel(ctx context.Context) (turn, bool) {
	t := turn{index: e.d.State.NextIteration()}
	max := e.d.State.MaxIterations()

	e.d.Logger.Debug("agent loop iteration", slog.Int("iteration", t.index), slog.Int("max", max))
	e.d.Emitter.Emit(event.ThinkingData{Message: thinkingMessage(t.index, max)})

	t.request = e.d.Compactor.Compact(e.d.State.Messages())
	t.usageBefore = e.d.State.Usage()

	start := time.Now()
	reply, err := e.d.Caller.Call(ctx, e.d.Model, t.request)
	t.took = time.Since(start)

	if err != nil {
		e.d.Finish.Fail(err.Error())
		return turn{}, false
	}
	if reply == nil {
		e.d.Finish.Fail("empty response from LLM")
		return turn{}, false
	}
	e.d.State.AddUsage(reply)
	t.reply = reply
	return t, true
}

// recordTurn emits the LLMTurn audit event for a turn that led to tool calls.
func (e *Engine) recordTurn(t turn) {
	usage := e.d.State.Usage()
	e.d.Emitter.Emit(e.d.Audit.Record(audit.Turn{
		Index:         t.index,
		InputTokens:   usage.Input - t.usageBefore.Input,
		OutputTokens:  usage.Output - t.usageBefore.Output,
		Request:       t.request,
		Reply:         t.reply,
		DurationMs:    t.took.Milliseconds(),
		MaxIterations: e.d.State.MaxIterations(),
	}))
}
