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

// Cursor moves (ANSI): up one line, and down one line, each to column 0.
const (
	cursorUp   = "\x1b[1A\r"
	cursorDown = "\x1b[1B\r"
)

// Next returns the next prompt, or false when the session is over.
func (t Terminal) Next(ctx context.Context) (string, bool) {
	if t.isTerminal() {
		return t.boxed(ctx)
	}
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

// boxed is Next on a terminal: both rules are drawn before typing starts, and
// the cursor goes back up between them. Enter leaves the cursor on the bottom
// rule, so a typed line steps past it and an empty one goes back up again.
// A line longer than the terminal wraps onto the bottom rule.
func (t Terminal) boxed(ctx context.Context) (string, bool) {
	fmt.Fprintln(t.stderr, t.line())
	fmt.Fprintln(t.stderr)
	fmt.Fprint(t.stderr, t.line()+cursorUp)
	for {
		fmt.Fprint(t.stderr, t.caret.Render("❯")+" ")
		select {
		case <-ctx.Done():
			fmt.Fprint(t.stderr, cursorDown+"\n") // leave the box
			return "", false
		case line, ok := <-t.lines:
			if !ok {
				fmt.Fprint(t.stderr, cursorDown+"\n")
				return "", false
			}
			if text := strings.TrimSpace(line); text != "" {
				fmt.Fprintln(t.stderr)
				return text, true
			}
			fmt.Fprint(t.stderr, cursorUp)
		}
	}
}

// fd is stderr's file descriptor, when it has one.
func (t Terminal) fd() (uintptr, bool) {
	f, ok := t.stderr.(interface{ Fd() uintptr })
	if !ok {
		return 0, false
	}
	return f.Fd(), true
}

func (t Terminal) isTerminal() bool {
	fd, ok := t.fd()
	return ok && term.IsTerminal(fd)
}

// line is a faint rule as wide as stderr, read each time so a resize shows.
func (t Terminal) line() string {
	width := defaultWidth
	if fd, ok := t.fd(); ok {
		if w, _, err := term.GetSize(fd); err == nil && w > 0 {
			width = w
		}
	}
	return t.rule.Render(strings.Repeat("─", width))
}
