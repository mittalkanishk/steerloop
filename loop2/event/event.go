// Package event is the vocabulary of a running loop: every event type and
// payload. It is a leaf (it imports nothing from loop2), so the public API
// and every internal package can share it without an import cycle.
package event

import "time"

// Type classifies an event.
type Type string

const (
	// Thinking is emitted before each model call.
	Thinking Type = "thinking"
	// ToolCall is emitted when the model asks for a tool, before it runs.
	ToolCall Type = "tool_call"
	// ToolResult is emitted when a tool finishes, with its result or error.
	ToolResult Type = "tool_result"
	// Response is emitted when the model replies with text and no tool call.
	Response Type = "response"
	// Reflection is emitted for the optional self-review reply.
	Reflection Type = "reflection"
	// Progress is a human-readable status message.
	Progress Type = "progress"
	// Error reports a failure. ErrorData.Fatal says whether the loop stopped.
	Error Type = "error"
	// Done is the last event. DoneData.Reason says why the loop ended.
	Done Type = "done"
	// LLMTurn is the audit record of one model call that led to tool calls.
	LLMTurn Type = "llm_turn"
	// AwaitingInput is emitted by a pause handler waiting for a reply. The
	// loop has not ended.
	AwaitingInput Type = "awaiting_input"
)

// Data is an event payload. Each payload names its own Type, so a payload
// can never be sent under another type's label.
type Data interface {
	Type() Type
}

// Event is one update from a running loop. Type always equals Data.Type();
// it is kept for JSON readers, which cannot see the payload's Go type.
type Event struct {
	Type      Type      `json:"type"`
	Data      Data      `json:"data"`
	Iteration int       `json:"iteration"`
	Timestamp time.Time `json:"timestamp"`
}

// New builds the event for data, labelled with data's own Type.
func New(data Data, iteration int) Event {
	return Event{Type: data.Type(), Data: data, Iteration: iteration, Timestamp: time.Now().UTC()}
}
