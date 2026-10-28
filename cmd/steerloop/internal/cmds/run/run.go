package runcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/bootstrap"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cli"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/interrupt"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/render"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/stdin"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/steering"
	"github.com/mittalkanishk/steerloop/internal/eventlog"
	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
)

// EventSink receives every event the loop emits.
// → cmd/steerloop/internal/render (people), steerloop/internal/eventlog (machines)
type EventSink interface {
	Write(evt event.Event)
}

// InputReader reads stdin into the lines steering applies.
// → cmd/steerloop/internal/stdin
type InputReader interface {
	Read(ctx context.Context)
}

// SteeringSource feeds a person's input into a running loop.
// → cmd/steerloop/internal/steering
type SteeringSource interface {
	Steer(ctx context.Context, loop *loop.Loop)
}

// Run is one fully wired run, ready to execute. NewRun builds it.
type Run struct {
	loop        *loop.Loop
	messages    []*schema.Message
	sinks       []EventSink          // every event goes to each: the screen, then the recording if any
	recording   *bootstrap.Recording // nil: not recording
	input       InputReader
	steering    SteeringSource
	controlFile *os.File // what steering reads with --control-file; nil without
	interrupts  *interrupt.Watcher
	stderr      io.Writer
}

// NewRun builds a Run from options. It is the only place that picks a
// concrete type for each interface run.go declares, so this function alone
// tells you which implementation runs.
func NewRun(ctx context.Context, env cli.Environment, opts Options) (*Run, error) {

	// Someone can answer an ask_user or confirm question: a person at the
	// terminal, or a program writing inject lines to --control-file.
	humanAvailable := env.StdinIsTerminal() || opts.ControlPath != ""

	loops, err := bootstrap.NewLoops(ctx, env, opts.Options, humanAvailable)
	if err != nil {
		return nil, err
	}

	loop, err := loops.New()
	if err != nil {
		return nil, err
	}

	messages, err := bootstrap.InitialMessages(opts.Options)
	if err != nil {
		return nil, err
	}

	// EventSink: JSONL for machines, text for people.
	var sink EventSink
	if opts.Output == "jsonl" {
		sink = eventlog.NewJSONL(env.Stdout())
	} else {
		sink = render.NewText(env.Stderr(), env.Stdout(), loops.StopCondition())
	}

	// InputReader and SteeringSource: the --control-file when given, else
	// the lines typed at the terminal when a person is there, nothing
	// otherwise.
	var input InputReader = stdin.None{}
	var steer SteeringSource = steering.None{}
	var controlFile *os.File
	switch {
	case opts.ControlPath != "":
		if controlFile, err = openControlFile(opts.ControlPath); err != nil {
			return nil, err
		}
		steer = steering.NewControl(controlFile, env.Stderr())
	case env.StdinIsTerminal():
		lines := stdin.NewLines(env.Stdin(), env.Stderr())
		input = lines
		steer = steering.NewTerminal(lines.C(), env.Stderr())
	}

	sinks := []EventSink{sink}

	// Recording: a second sink, opened last so no earlier failure leaves an
	// empty file behind. An empty --output-record-path turns it off.
	var recording *bootstrap.Recording
	if opts.OutputRecordPath != "" {
		if recording, err = bootstrap.OpenRecording(opts.OutputRecordPath, time.Now()); err != nil {
			if controlFile != nil {
				_ = controlFile.Close()
			}
			return nil, err
		}
		sinks = append(sinks, recording)
	}

	return &Run{
		loop:        loop,
		messages:    messages,
		sinks:       sinks,
		recording:   recording,
		input:       input,
		steering:    steer,
		controlFile: controlFile,
		interrupts:  interrupt.NewWatcher(env.Stderr()),
		stderr:      env.Stderr(),
	}, nil
}

// Execute runs one turn. Exactly four goroutines take part, all started here:
//
//  1. the loop itself  — loop.Start; it closes events when it is done
//  2. stdin lines      — r.input.Read; blocks on stdin, so it can outlive
//     the run (the process exits right after, so that is harmless)
//  3. steering input   — r.steering.Steer
//  4. Ctrl-C handling  — r.interrupts.Watch
//
// cancel (deferred) ends 3 and 4 once the loop has finished.
func (r *Run) Execute(parent context.Context) int {

	ctx, cancel := context.WithCancel(parent)

	defer cancel()

	defer r.closeRecording()

	// Closing the control file ends goroutine 3 when it is blocked reading a
	// FIFO that nobody writes to any more.
	defer r.closeControl()

	events := r.loop.Start(ctx, r.messages)

	go r.input.Read(ctx)

	go r.steering.Steer(ctx, r.loop)

	r.interrupts.Track(r.loop)
	go r.interrupts.Watch(ctx, cancel)

	// Ends when the loop closes the channel after its final Done event.
	for evt := range events {
		for _, sink := range r.sinks {
			sink.Write(evt)
		}
	}

	if r.interrupts.Aborted() {
		return cli.ExitStopped
	}

	result, _ := r.loop.Result()

	return cli.ExitCodeFor(result.Reason)
}

// closeRecording closes the recording, if any, and says where it is.
func (r *Run) closeRecording() {
	if r.recording == nil {
		return
	}
	if err := r.recording.Close(); err != nil {
		fmt.Fprintf(r.stderr, "steerloop: record: %v\n", err)
		return
	}
	fmt.Fprintf(r.stderr, "recorded to %s\n", r.recording.Path())
}

// closeControl closes the --control-file, if any.
func (r *Run) closeControl() {
	if r.controlFile != nil {
		_ = r.controlFile.Close()
	}
}
