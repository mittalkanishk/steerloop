package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mittalkanishk/steerloop"
)

// send retries briefly when the control queue is full, so a line typed
// during a long model call is not lost.
func (c *controller) send(ctx context.Context, ctrl steerloop.Control) {
	for i := 0; i < 200; i++ {
		if c.loop.SendControlSignal(ctrl) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(25 * time.Millisecond):
		}
	}
	fmt.Fprintln(c.stderr, "steerloop: control queue is full; input dropped")
}

// line handles one line typed at the terminal: a slash command, or text to
// inject as a user message.
func (c *controller) line(ctx context.Context, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if !strings.HasPrefix(text, "/") {
		c.send(ctx, steerloop.NewInjectControl(text))
		return
	}

	fields := strings.Fields(text)
	arg := ""
	if len(fields) > 1 {
		arg = fields[1]
	}
	switch fields[0] {
	case "/stop":
		c.loop.Stop()
	case "/max":
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 {
			fmt.Fprintln(c.stderr, "usage: /max <positive integer>")
			return
		}
		c.send(ctx, steerloop.NewModifyConfigControl(steerloop.ModifyConfigData{MaxIterations: &n}))
	case "/temp":
		x, err := strconv.ParseFloat(arg, 64)
		if err != nil || x < 0 {
			fmt.Fprintln(c.stderr, "usage: /temp <number>")
			return
		}
		c.send(ctx, steerloop.NewModifyConfigControl(steerloop.ModifyConfigData{Temperature: &x}))
	case "/reflect":
		if arg != "on" && arg != "off" {
			fmt.Fprintln(c.stderr, "usage: /reflect on|off")
			return
		}
		on := arg == "on"
		c.send(ctx, steerloop.NewModifyConfigControl(steerloop.ModifyConfigData{EnableReflection: &on}))
	default:
		fmt.Fprintf(c.stderr, "unknown command %s (use /stop, /max, /temp, /reflect)\n", fields[0])
	}
}

// scanLines sends each line from r. The channel closes at EOF.
func scanLines(r io.Reader) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			ch <- sc.Text()
		}
	}()
	return ch
}

// forwardLines applies terminal lines to this turn until stop is closed or
// lines ends. One scanner feeds both this and the next-prompt wait, so a line
// is handled by whichever is receiving.
func (c *controller) forwardLines(ctx context.Context, lines <-chan string, stop <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			c.line(ctx, line)
		}
	}
}

// readControls applies JSONL controls from a file or FIFO.
func (c *controller) readControls(ctx context.Context, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var cl controlLine
		dec := json.NewDecoder(strings.NewReader(sc.Text()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cl); err != nil {
			fmt.Fprintf(c.stderr, "steerloop: control line %d ignored: %v\n", n, err)
			continue
		}
		if cl.WaitMs > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(cl.WaitMs) * time.Millisecond):
			}
		}
		if ctx.Err() != nil {
			return
		}
		switch steerloop.ControlType(cl.Type) {
		case steerloop.ControlStop:
			c.loop.Stop()
		case steerloop.ControlInject:
			c.send(ctx, steerloop.NewInjectControl(cl.Message))
		case steerloop.ControlModifyConfig:
			c.send(ctx, steerloop.NewModifyConfigControl(steerloop.ModifyConfigData{
				MaxIterations:    cl.MaxIterations,
				Temperature:      cl.Temperature,
				EnableReflection: cl.EnableReflection,
			}))
		default:
			fmt.Fprintf(c.stderr, "steerloop: control line %d ignored: unknown type %q\n", n, cl.Type)
		}
	}
}
