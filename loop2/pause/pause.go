// Package pause is what a pause handler sees and can do. It is a public leaf
// package rather than part of loop2, because both callers (who write
// handlers) and loop2/internal/dispatch (which runs them) need these types,
// and a shared leaf is the only way to share them without an import cycle.
package pause

import (
	"context"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/event"
)

// Call identifies the tool call a Handler is handling.
type Call struct {
	ID        string
	Name      string
	Arguments string
}

// Session is what a Handler can do while it handles a call.
// → loop2/internal/dispatch/session.go
type Session interface {
	// Messages is a copy of the conversation so far.
	Messages() []*schema.Message
	// Tools are the tools the loop was created with.
	Tools() []*schema.ToolInfo
	// Execute runs a tool through the loop's ToolExecutor.
	Execute(ctx context.Context, name, args string) (string, error)
	// CallModel makes a model call with the loop's retry and timeout
	// handling. A non-empty tools list binds the model to those tools for
	// this call.
	CallModel(ctx context.Context, tools []*schema.ToolInfo, msgs []*schema.Message) (*schema.Message, error)
	// Emit sends an event on the loop's event channel.
	Emit(t event.Type, data any)
	// AwaitReply waits for the next injected message and returns it. ok is
	// false when the loop was stopped or ctx ended while waiting.
	AwaitReply(ctx context.Context) (reply string, ok bool)
}

// Handler handles calls to one tool in place of the ToolExecutor. The loop
// waits for Handle to return; reply becomes the tool's result. Returning
// keepGoing false ends the run: as awaiting a human if the handler emitted
// event.AwaitingInput, otherwise as stopped.
type Handler interface {
	Handle(ctx context.Context, call Call, s Session) (reply string, keepGoing bool)
}
