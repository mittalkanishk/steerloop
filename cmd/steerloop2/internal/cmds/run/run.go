package runcmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cli"
	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/interrupt"
	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/render"
	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/steering"
	"github.com/mittalkanishk/steerloop/internal/eventlog"
	"github.com/mittalkanishk/steerloop/internal/provider"
	"github.com/mittalkanishk/steerloop/loop2"
	"github.com/mittalkanishk/steerloop/loop2/event"
)

// EventSink receives every event the loop emits.
// → cmd/steerloop2/internal/render (people), steerloop/internal/eventlog (machines)
type EventSink interface {
	Write(evt event.Event)
}

// SteeringSource feeds a person's input into a running loop.
// → cmd/steerloop2/internal/steering
type SteeringSource interface {
	Steer(ctx context.Context, loop *loop2.Loop)
}

// Run is one fully wired run, ready to execute. wire.go builds it.
type Run struct {
	loop        *loop2.Loop
	messages    []*schema.Message
	sinks       []EventSink // every event goes to each: the screen, then the recording if any
	recording   *Recording  // nil: not recording
	steering    SteeringSource
	controlFile *os.File // what steering reads with --control-file; nil without
	interrupts  *interrupt.Watcher
	stderr      io.Writer
}

// Wire builds a Run from options. It is the only place that picks a concrete
// type for each interface run.go declares, so this function alone tells you
// which implementation runs.
func NewRun(ctx context.Context, env cli.Environment, opts Options) (*Run, error) {

	llm, err := provider.Build(ctx, opts.Model, provider.Options{Getenv: env.Getenv, MaxTokens: opts.MaxTokens})
	if err != nil {
		return nil, err
	}

	// Someone can answer an ask_user or confirm question: a person at the
	// terminal, or a program writing inject lines to --control-file.
	humanAvailable := env.StdinIsTerminal() || opts.ControlPath != ""

	tools, err := NewToolset(opts, humanAvailable)
	if err != nil {
		return nil, err
	}

	cfg := loop2.DefaultConfig()
	cfg.MaxIterations = opts.MaxIterations
	cfg.Pauses = tools.Gates
	cfg.StopCondition = stopConditions[opts.StopCondition] // basically a map of 3 accepted values.
	cfg.Reflect = reflectConditions[opts.Reflect]
	cfg.Temperature = opts.Temperature
	cfg.IterationTimeout = opts.IterationTimeout
	cfg.TotalTimeout = opts.TotalTimeout

	// Logger: the loop's debug log on stderr with --debug; nil discards it.
	var logger *slog.Logger
	if opts.Debug {
		logger = slog.New(slog.NewTextHandler(env.Stderr(), &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	loop, err := loop2.New(cfg, llm.Chat, tools.Infos, tools.Executor, logger)
	if err != nil {
		return nil, err
	}

	messages, err := initialMessages(opts)
	if err != nil {
		return nil, err
	}

	// EventSink: JSONL for machines, text for people.
	var sink EventSink
	if opts.Output == "jsonl" {
		sink = eventlog.NewJSONL(env.Stdout())
	} else {
		sink = render.NewText(env.Stderr(), env.Stdout(), cfg.StopCondition)
	}

	// SteeringSource: the --control-file when given, else the terminal when a
	// person is there, nothing otherwise.
	var steer SteeringSource = steering.None{}
	var controlFile *os.File
	switch {
	case opts.ControlPath != "":
		if controlFile, err = openControlFile(opts.ControlPath); err != nil {
			return nil, err
		}
		steer = steering.NewControl(controlFile, env.Stderr())
	case env.StdinIsTerminal():
		steer = steering.NewTerminal(env.Stdin(), env.Stderr())
	}

	sinks := []EventSink{sink}

	// Recording: a second sink, opened last so no earlier failure leaves an
	// empty file behind. An empty --output-record-path turns it off.
	var recording *Recording
	if opts.OutputRecordPath != "" {
		if recording, err = OpenRecording(opts.OutputRecordPath, time.Now()); err != nil {
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
		steering:    steer,
		controlFile: controlFile,
		interrupts:  interrupt.NewWatcher(env.Stderr()),
		stderr:      env.Stderr(),
	}, nil
}

// initialMessages is the conversation the run starts from: the --system-prompt
// file, if any, then the user prompt.
func initialMessages(opts Options) ([]*schema.Message, error) {
	var messages []*schema.Message
	if opts.SystemPromptPath != "" {
		system, err := os.ReadFile(opts.SystemPromptPath)
		if err != nil {
			return nil, fmt.Errorf("read system prompt: %w", err)
		}
		if strings.TrimSpace(string(system)) == "" {
			return nil, fmt.Errorf("system prompt file %s is empty", opts.SystemPromptPath)
		}
		messages = append(messages, &schema.Message{
			Role:    schema.System,
			Content: string(system),
		})
	}

	// User Prompt is always added.
	// So you get [system, user] with --system-prompt and [user] without it.
	messages = append(messages, &schema.Message{
		Role:    schema.User,
		Content: strings.TrimSpace(opts.UserPrompt),
	})

	return messages, nil
}

// Execute runs one turn. Exactly three goroutines take part, all started here:
//
//  1. the loop itself     — loop.Start; it closes events when it is done
//  2. steering input      — r.steering.Steer
//  3. Ctrl-C handling     — r.interrupts.Watch
//
// cancel (deferred) ends 2 and 3 once the loop has finished.
func (r *Run) Execute(parent context.Context) int {

	ctx, cancel := context.WithCancel(parent)

	defer cancel()

	defer r.closeRecording()

	// Closing the control file ends goroutine 2 when it is blocked reading a
	// FIFO that nobody writes to any more.
	defer r.closeControl()

	events := r.loop.Start(ctx, r.messages)

	go r.steering.Steer(ctx, r.loop)

	go r.interrupts.Watch(ctx, cancel, r.loop)

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
		fmt.Fprintf(r.stderr, "steerloop2: record: %v\n", err)
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
