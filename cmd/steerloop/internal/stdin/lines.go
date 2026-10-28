// Package stdin reads the terminal once for a whole run or chat.
package stdin

import (
	"bufio"
	"context"
	"fmt"
	"io"
)

// Lines is the one reader of stdin. Each line typed goes out on C, to
// whoever is listening at the time: steering while a turn runs and, in a
// chat, the prompt between turns. C is unbuffered, so a line typed while
// nobody listens waits for the next listener instead of being lost.
type Lines struct {
	in     io.Reader
	stderr io.Writer
	out    chan string // Read owns it and closes it at end of input
}

func NewLines(in io.Reader, stderr io.Writer) *Lines {
	return &Lines{in: in, stderr: stderr, out: make(chan string)}
}

// C is the channel of typed lines. It is closed when stdin ends.
func (l *Lines) C() <-chan string { return l.out }

// Read sends each line on C until stdin ends or ctx ends. Scan blocks on
// stdin and cannot be cancelled, so this goroutine can outlive the session;
// the process exits right after, so that is harmless.
func (l *Lines) Read(ctx context.Context) {
	defer close(l.out)
	scanner := bufio.NewScanner(l.in)
	for scanner.Scan() {
		select {
		case l.out <- scanner.Text():
		case <-ctx.Done():
			return
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(l.stderr, "steerloop: reading input: %v\n", err)
	}
}
