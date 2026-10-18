// Package stopping holds one stop policy per stop condition. wire.go picks
// one, so the engine never asks which mode it is in.
package stopping

import "github.com/cloudwego/eino/schema"

// NoToolCalls ends the run when the model replies without calling a tool.
// Suits chat and question answering.
type NoToolCalls struct{}

func (NoToolCalls) Label() string { return "no_tool_calls" }

func (NoToolCalls) FinishCall(*schema.Message) (schema.ToolCall, bool) {
	return schema.ToolCall{}, false
}

func (NoToolCalls) AfterText() (ends bool, nudge string) { return true, "" }
