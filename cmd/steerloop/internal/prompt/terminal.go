// Package prompt asks for the next turn once an answer is in.
package prompt

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

// defaultWidth is the rule's width when stderr's width is unknown.
const defaultWidth = 80

// Terminal frames the input between two faint rules, prints "❯ " on stderr
// and waits for the next line typed. An empty line asks again. End of input,
// or ctx ending (Ctrl-C at the prompt), ends the session.
type Terminal struct {
	lines       <-chan string // from stdin.Lines, shared with steering
	stderr      io.Writer
	rule, caret lipgloss.Style // bound to stderr, so colour turns off on its own
}

func NewTerminal(lines <-chan string, stderr io.Writer) Terminal {
	r := lipgloss.NewRenderer(stderr)
	return Terminal{
		lines:  lines,
		stderr: stderr,
		rule:   r.NewStyle().Faint(true),
		caret:  r.NewStyle().Bold(true).Foreground(lipgloss.Color("12")),
	}
}

// Next returns the next prompt, or false when the session is over.
func (t Terminal) Next(ctx context.Context) (string, bool) {
	fmt.Fprintln(t.stderr, t.line())
	for {
		fmt.Fprint(t.stderr, t.caret.Render("❯")+" ")
		select {
		case <-ctx.Done():
			return "", false
		case line, ok := <-t.lines:
			if !ok {
				fmt.Fprintln(t.stderr) // end the "❯ " line
				return "", false
			}
			if text := strings.TrimSpace(line); text != "" {
				fmt.Fprintln(t.stderr, t.line())
				return text, true
			}
		}
	}
}

// line is a faint rule as wide as stderr, read each time so a resize shows.
func (t Terminal) line() string {
	width := defaultWidth
	if f, ok := t.stderr.(interface{ Fd() uintptr }); ok {
		if w, _, err := term.GetSize(f.Fd()); err == nil && w > 0 {
			width = w
		}
	}
	return t.rule.Render(strings.Repeat("─", width))
}
