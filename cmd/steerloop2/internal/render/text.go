// Package render prints a run for a person at a terminal.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/mittalkanishk/steerloop/loop2/event"
)

// Text writes the trace to one writer (stderr) and the final answer to
// another (stdout).
type Text struct {
	trace io.Writer
	final io.Writer
}

func NewText(trace, final io.Writer) *Text {
	return &Text{trace: trace, final: final}
}

func (t *Text) Write(evt event.Event) {
	switch d := evt.Data.(type) {
	case event.ToolCallData:
		fmt.Fprintf(t.trace, "  → %s %s\n", d.Name, d.Arguments)
	case event.ToolResultData:
		fmt.Fprintf(t.trace, "  ← %s: %s\n", d.Name, d.Result)
	case event.AwaitingInputData:
		options := ""
		if len(d.Options) > 0 {
			options = " [" + strings.Join(d.Options, " / ") + "]"
		}
		fmt.Fprintf(t.trace, "  ? %s%s\n", d.Question, options)
	case event.ErrorData:
		fmt.Fprintf(t.trace, "  error: %s\n", d.Error)
	case event.DoneData:
		fmt.Fprintf(t.trace, "done: %s · %d step(s)\n", d.Reason, d.Iterations)
		if d.FinalContent != "" {
			fmt.Fprintln(t.final, d.FinalContent)
		}
	}
}
