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

	"github.com/Kanishkmittal55/steerloop"
)

// controller applies steering input to a running loop.
type controller struct {
	loop   *steerloop.Loop
	stderr io.Writer
}

// inject retries briefly when the control queue is full, so a line typed
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

// readLines feeds terminal lines to the controller until the reader ends.
func (c *controller) readLines(ctx context.Context, r io.Reader) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		c.line(ctx, sc.Text())
	}
}

// controlLine is one line of a --control file.
type controlLine struct {
	Type             string   `json:"type"`
	Message          string   `json:"message"`
	MaxIterations    *int     `json:"max_iterations"`
	Temperature      *float64 `json:"temperature"`
	EnableReflection *bool    `json:"enable_reflection"`
	// WaitMs delays this control, counted from the previous one.
	WaitMs int `json:"wait_ms"`
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
