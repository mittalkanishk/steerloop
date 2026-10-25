package steering

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mittalkanishk/steerloop/loop2"
)

const (
	// maxControlLine caps one control line; an inject message can be long.
	maxControlLine = 1024 * 1024

	// deliverFor is how long a command keeps retrying while the loop's
	// control queue is full, so a burst of lines from a file is not lost.
	deliverFor   = 5 * time.Second
	deliverEvery = 25 * time.Millisecond
)

// Control turns JSONL lines from a file or FIFO into steering, so a program
// can steer a run the way a person does at the terminal. One line is one
// command:
//
//	{"type":"stop"}
//	{"type":"inject","message":"focus on go.mod"}
//	{"type":"modify_config","max_iterations":20,"temperature":0.1}
//
// wait_ms on any line delays it, counted from the previous line. An inject
// is also how an ask_user or confirm question gets its answer. A bad line is
// reported and skipped; it never ends the run.
type Control struct {
	in     io.Reader
	stderr io.Writer
}

func NewControl(in io.Reader, stderr io.Writer) Control {
	return Control{in: in, stderr: stderr}
}

// command is one line of a control file.
type command struct {
	Type          string   `json:"type"`
	Message       string   `json:"message"`
	MaxIterations *int     `json:"max_iterations"`
	Temperature   *float64 `json:"temperature"`
	WaitMs        int      `json:"wait_ms"`
}

// Steer applies lines until the input ends or ctx ends. A FIFO never ends
// on its own; closing the file (run does, after the loop) ends the read.
func (c Control) Steer(ctx context.Context, loop *loop2.Loop) {
	sc := bufio.NewScanner(c.in)
	sc.Buffer(make([]byte, 0, 64*1024), maxControlLine)
	for n := 1; sc.Scan(); n++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		cmd, err := parseCommand(text)
		if err != nil {
			fmt.Fprintf(c.stderr, "steerloop2: control line %d ignored: %v\n", n, err)
			continue
		}
		if !sleep(ctx, time.Duration(cmd.WaitMs)*time.Millisecond) {
			return
		}
		c.apply(ctx, loop, cmd)
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil && !errors.Is(err, os.ErrClosed) {
		fmt.Fprintf(c.stderr, "steerloop2: reading controls: %v\n", err)
	}
}

func (c Control) apply(ctx context.Context, loop *loop2.Loop, cmd command) {
	switch cmd.Type {
	case "stop":
		loop.Stop() // never lost to a full queue
	case "inject":
		c.deliver(ctx, func() bool { return loop.Inject(cmd.Message) })
	case "modify_config":
		change := loop2.ConfigChange{MaxIterations: cmd.MaxIterations, Temperature: cmd.Temperature}
		c.deliver(ctx, func() bool { return loop.ModifyConfig(change) })
	}
}

// deliver retries send while the control queue is full, for up to deliverFor.
func (c Control) deliver(ctx context.Context, send func() bool) {
	for range deliverFor / deliverEvery {
		if send() {
			return
		}
		if !sleep(ctx, deliverEvery) {
			return
		}
	}
	fmt.Fprintln(c.stderr, "steerloop2: control queue is full; command dropped")
}

// parseCommand decodes one line and checks it carries what its type needs.
// Unknown fields are rejected, so a typo is reported rather than ignored.
func parseCommand(text string) (command, error) {
	var cmd command
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cmd); err != nil {
		return command{}, err
	}
	if cmd.WaitMs < 0 {
		return command{}, errors.New("wait_ms must not be negative")
	}
	switch cmd.Type {
	case "stop":
	case "inject":
		if strings.TrimSpace(cmd.Message) == "" {
			return command{}, errors.New(`inject needs a non-empty "message"`)
		}
	case "modify_config":
		if cmd.MaxIterations == nil && cmd.Temperature == nil {
			return command{}, errors.New(`modify_config needs "max_iterations" or "temperature"`)
		}
		if cmd.MaxIterations != nil && *cmd.MaxIterations < 1 {
			return command{}, errors.New("max_iterations must be at least 1")
		}
	default:
		return command{}, fmt.Errorf("unknown type %q (use stop, inject or modify_config)", cmd.Type)
	}
	return cmd, nil
}

// sleep waits for d and reports false if ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
