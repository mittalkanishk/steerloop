package stopping_policy

import (
	"fmt"

	"github.com/cloudwego/eino/schema"
)

// FinishTool runs until the model calls the finish tool; its arguments are
// the final output. A text-only reply is the model narrating instead of
// acting, so it is nudged back to a tool call.
type FinishTool struct {
	toolName string
	nudge    string // empty: no nudge
}

func NewFinishTool(toolName, nudge string) FinishTool {
	return FinishTool{
		toolName: toolName,
		nudge:    nudge}
}

func (FinishTool) Label() string { return "finish_tool" }

// finishtool.go
func (f FinishTool) Instructions() string {
	return fmt.Sprintf("The run ends only when you call the %s tool, with your final "+
		"result as its argument. A reply without a tool call does not end the run.", f.toolName)
}

func (f FinishTool) FinishCall(reply *schema.Message) (schema.ToolCall, bool) {
	for _, tc := range reply.ToolCalls {
		if tc.Function.Name == f.toolName {
			return tc, true
		}
	}
	return schema.ToolCall{}, false
}

func (f FinishTool) AfterText() (ends bool, nudge string) { return false, f.nudge }
