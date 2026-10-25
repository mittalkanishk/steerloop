package engine

import (
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop/event"
)

// Engine implements control.Notifier: Inbox.Drain calls these for controls
// applied between iterations.

// Injected adds the caller's message to the conversation.
func (e *Engine) Injected(message string) {

	e.d.State.Append(&schema.Message{
		Role:    schema.User,
		Content: message,
	})

	e.d.Emitter.Emit(event.ProgressData{
		Message: "User instruction received: " + message,
		Step:    "inject",
	})
}

// ConfigChanged reports a settings change; the inbox has already applied it.
func (e *Engine) ConfigChanged() {

	e.d.Emitter.Emit(event.ProgressData{
		Message: "Configuration updated",
		Step:    "config_modify",
	})
}
