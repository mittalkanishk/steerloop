package engine

import (
	"context"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/event"
)

// reflect asks the model to review its final reply. A failure is reported
// but does not end the run.
func (e *Engine) reflect(ctx context.Context, content string) {
	e.d.State.Append(&schema.Message{
		Role:    schema.User,
		Content: e.d.ReflectionPrompt + "\n\nYour response was:\n" + content,
	})

	reflection, err := e.d.Caller.Call(ctx, e.d.Model, e.d.State.Messages())
	if err != nil {
		e.d.Emitter.Emit(event.Error, event.ErrorData{Error: "reflection failed: " + err.Error()})
		return
	}
	if reflection == nil {
		return
	}
	e.d.State.AddUsage(reflection)
	e.d.Emitter.Emit(event.Reflection, event.ReflectionData{Content: reflection.Content})
	e.d.State.Append(reflection)
}
