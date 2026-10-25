package reflection

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// maxResult caps each tool result shown to the judge, so one large output
// does not dominate the review's cost.
const maxResult = 2000

// brief lays out the task and the work under review for the judge.
func brief(task []*schema.Message, label, work string) string {
	var b strings.Builder
	for _, m := range task {
		heading := "User request"
		if m.Role == schema.System {
			heading = "System prompt"
		}
		fmt.Fprintf(&b, "%s:\n%s\n\n", heading, m.Content)
	}
	fmt.Fprintf(&b, "%s:\n%s", label, work)
	return b.String()
}

// describe writes an iteration's messages as plain text. Tool calls become
// text, so the judge's request holds no tool calls and needs no tools bound.
func describe(msgs []*schema.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		switch m.Role {
		case schema.Assistant:
			if m.Content != "" {
				fmt.Fprintf(&b, "The model said: %s\n", m.Content)
			}
			for _, tc := range m.ToolCalls {
				fmt.Fprintf(&b, "The model called %s(%s)\n", tc.Function.Name, tc.Function.Arguments)
			}
		case schema.Tool:
			fmt.Fprintf(&b, "%s returned: %s\n", m.ToolName, clip(m.Content, maxResult))
		default:
			fmt.Fprintf(&b, "%s: %s\n", m.Role, m.Content)
		}
	}
	return b.String()
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
