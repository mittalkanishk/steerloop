// Package render prints a run for a person at a terminal.
package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/mittalkanishk/steerloop/loop2"
	"github.com/mittalkanishk/steerloop/loop2/event"
)

// maxPreview is how many characters of an argument, result or reply fit on
// one trace line.
const maxPreview = 160

// styles colour the trace. They are bound to the trace writer, so colour
// turns off on its own when stderr is not a terminal or NO_COLOR is set.
type styles struct {
	step, tool, dim, ok, fail, ask, reply lipgloss.Style
}

func newStyles(w io.Writer) styles {
	r := lipgloss.NewRenderer(w)
	return styles{
		step:  r.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
		tool:  r.NewStyle().Bold(true).Foreground(lipgloss.Color("14")),
		dim:   r.NewStyle().Faint(true),
		ok:    r.NewStyle().Foreground(lipgloss.Color("10")),
		fail:  r.NewStyle().Bold(true).Foreground(lipgloss.Color("9")),
		ask:   r.NewStyle().Bold(true).Foreground(lipgloss.Color("11")),
		reply: r.NewStyle().Italic(true),
	}
}

// Text writes the trace to one writer (stderr) and the final answer, unstyled
// so it pipes cleanly, to another (stdout).
type Text struct {
	trace io.Writer
	final io.Writer
	stop  loop2.StopCondition
	s     styles
}

func NewText(trace, final io.Writer, stop loop2.StopCondition) *Text {
	return &Text{
		trace: trace,
		final: final,
		stop:  stop,
		s:     newStyles(trace),
	}
}

func (t *Text) Write(evt event.Event) {

	switch d := evt.Data.(type) {

	case event.ThinkingData:
		fmt.Fprintf(t.trace, "\n%s\n", t.s.step.Render(fmt.Sprintf("── step %d ", evt.Iteration)))

	case event.ToolCallData:
		fmt.Fprintf(t.trace, "  %s %s\n", t.s.tool.Render("▸ "+d.Name), t.s.dim.Render(oneLine(d.Arguments)))

	case event.ToolResultData:
		if d.Error != "" {
			fmt.Fprintf(t.trace, "  %s %s\n", t.s.fail.Render("✗ "+d.Name), oneLine(d.Error))
			return
		}
		fmt.Fprintf(t.trace, "  %s %s\n", t.s.ok.Render("✓ "+d.Name), t.s.dim.Render(oneLine(d.Result)))

	case event.AwaitingInputData:
		options := ""
		if len(d.Options) > 0 {
			options = " [" + strings.Join(d.Options, " / ") + "]"
		}
		fmt.Fprintf(t.trace, "  %s%s\n", t.s.ask.Render("? "+d.Question), t.s.dim.Render(options))

	case event.ErrorData:
		fmt.Fprintf(t.trace, "  %s\n", t.s.fail.Render("error: "+d.Error))

	case event.ResponseData:
		// Under no-tool-calls the reply is the final answer, printed at Done.
		if t.stop != loop2.StopOnNoToolCalls {
			fmt.Fprintf(t.trace, "  %s\n", t.s.reply.Render("│ "+oneLine(d.Content)))
		}

	case event.DoneData:
		mark := t.s.ok.Render("● " + string(d.Reason))
		if d.Reason != event.ReasonFinished {
			mark = t.s.fail.Render("● " + string(d.Reason))
		}
		fmt.Fprintf(t.trace, "\n%s %s\n\n", mark, t.s.dim.Render(fmt.Sprintf("· %d step(s) · %d tokens", d.Iterations, d.LLMUsage.TotalTokens)))
		if d.FinalContent != "" {
			fmt.Fprintln(t.final, d.FinalContent)
		}
	}
}

// oneLine collapses whitespace and cuts s to maxPreview characters.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if runes := []rune(s); len(runes) > maxPreview {
		return string(runes[:maxPreview]) + "…"
	}
	return s
}
