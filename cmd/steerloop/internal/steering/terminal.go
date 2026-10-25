// Package steering feeds a person's input into a running loop.
package steering

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mittalkanishk/steerloop/loop"
)

// Terminal turns each line typed during the run into steering. "/stop" stops
// the loop; anything else is injected as a user message, which is also how
// an ask_user or confirm question (steerloop/internal/gates) gets its answer.
type Terminal struct {
	in     io.Reader
	stderr io.Writer
}

func NewTerminal(in io.Reader, stderr io.Writer) Terminal {
	return Terminal{in: in, stderr: stderr}
}

// Steer reads lines until stdin closes or ctx ends. Scan blocks on stdin, so
// this goroutine can outlive the run; the process exits right after, so that
// is harmless here.
func (t Terminal) Steer(ctx context.Context, loop *loop.Loop) {
	scanner := bufio.NewScanner(t.in)
	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		text := strings.TrimSpace(scanner.Text())
		switch text {
		case "":
			continue
		case "/stop":
			loop.Stop()
		default:
			if !loop.Inject(text) {
				fmt.Fprintln(t.stderr, "steerloop: control queue is full; input dropped")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(t.stderr, "steerloop: reading input: %v\n", err)
	}
}
