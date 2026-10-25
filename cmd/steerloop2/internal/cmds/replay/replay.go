package replaycmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cli"
	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/render"
	"github.com/mittalkanishk/steerloop/internal/eventlog"
	"github.com/mittalkanishk/steerloop/loop2"
	"github.com/mittalkanishk/steerloop/loop2/event"
)

// EventSink shows each replayed event. → cmd/steerloop2/internal/render
type EventSink interface {
	Write(evt event.Event)
}

// Replay is one recording, ready to play. NewReplay builds it.
type Replay struct {
	events  []event.Event
	sink    EventSink
	clock   Clock
	stderr  io.Writer
	speed   float64       // 0: instant
	maxWait time.Duration // 0: no cap
}

// NewReplay reads the recording at opts.Path and picks the sink. It is the
// only place that picks a concrete type for each interface replay.go declares.
func NewReplay(env cli.Environment, clock Clock, opts Options) (*Replay, error) {

	f, err := os.Open(opts.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	events, err := eventlog.Read(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", opts.Path, err)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("%s: recording is empty", opts.Path)
	}

	// EventSink: the text renderer run uses, so a replay looks like the live run.
	sink := render.NewText(env.Stderr(), env.Stdout(), stopConditionOf(events))

	return &Replay{
		events:  events,
		sink:    sink,
		clock:   clock,
		stderr:  env.Stderr(),
		speed:   opts.Speed,
		maxWait: opts.MaxWait,
	}, nil
}

// stopConditionOf infers what render.Text needs from the recording, which
// does not store the stop condition. Text only uses it to avoid printing the
// final answer twice: when the last response is the final answer, the run
// behaved as no-tool-finish.
func stopConditionOf(events []event.Event) loop2.StopCondition {
	var lastResponse string
	for _, evt := range events {
		switch d := evt.Data.(type) {
		case event.ResponseData:
			lastResponse = d.Content
		case event.DoneData:
			if d.FinalContent != "" && d.FinalContent == lastResponse {
				return loop2.StopOnNoToolFinish
			}
		}
	}
	return loop2.StopOnFinishTool
}

// Execute plays the recording and returns the exit code the recorded run
// ended with. It starts no goroutine of its own: one loop waits, then
// writes, event by event. Ctrl-C cancels ctx, which ends the current wait.
func (r *Replay) Execute(parent context.Context) int {

	ctx, stop := signal.NotifyContext(parent, os.Interrupt)
	defer stop()

	code := cli.ExitError
	sawDone := false
	for i, evt := range r.events {
		if i > 0 {
			if wait := r.wait(r.events[i-1], evt); wait > 0 {
				if err := r.clock.Sleep(ctx, wait); err != nil {
					return cli.ExitStopped
				}
			}
		}

		r.sink.Write(evt)

		if done, ok := evt.Data.(event.DoneData); ok {
			code = cli.ExitCodeFor(done.Reason)
			sawDone = true
		}
	}

	if !sawDone {
		fmt.Fprintln(r.stderr, "steerloop2: recording has no done event")
	}
	return code
}

// wait is how long to pause before next: the recorded gap scaled by speed,
// capped at maxWait. Zero means no pause: an instant replay, or events that
// share or reverse a timestamp.
func (r *Replay) wait(prev, next event.Event) time.Duration {
	if r.speed == 0 {
		return 0
	}
	gap := next.Timestamp.Sub(prev.Timestamp)
	if gap <= 0 {
		return 0
	}
	wait := time.Duration(float64(gap) / r.speed)
	if r.maxWait > 0 && wait > r.maxWait {
		wait = r.maxWait
	}
	return wait
}
