// Package steering feeds a person's input into a running loop.
package steering

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mittalkanishk/steerloop/loop"
)

// Terminal turns each line typed during a turn into steering. "/stop" stops
// the loop; anything else is injected as a user message, which is also how
// an ask_user or confirm question (steerloop/internal/gates) gets its answer.
type Terminal struct {
	lines  <-chan string // from stdin.Lines; a chat shares them with the prompt between turns
	stderr io.Writer
}

func NewTerminal(lines <-chan string, stderr io.Writer) Terminal {
	return Terminal{lines: lines, stderr: stderr}
}

// Steer applies lines until ctx ends (run cancels it when the turn is over)
// or input ends. Returning on ctx is what hands the lines that follow to the
// prompt for the next turn.
func (t Terminal) Steer(ctx context.Context, loop *loop.Loop) {
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-t.lines:
			if !ok {
				return
			}
			t.apply(loop, strings.TrimSpace(line))
		}
	}
}

func (t Terminal) apply(loop *loop.Loop, text string) {
	switch text {
	case "":
	case "/stop":
		loop.Stop()
	default:
		if !loop.Inject(text) {
			fmt.Fprintln(t.stderr, "steerloop: control queue is full; input dropped")
		}
	}
}
