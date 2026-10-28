package chatcmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/bootstrap"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cli"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/interrupt"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/prompt"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/render"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/stdin"
	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/steering"
	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
)

// EventSink receives every event the loop emits.
// → cmd/steerloop/internal/render (the screen), bootstrap.Recording (the file)
type EventSink interface {
	Write(evt event.Event)
}

// Chat is one fully wired session at a terminal, ready to execute. NewChat
// builds it. A chat has one mode, a person typing, so it holds concrete
// types: one reader of stdin feeds both the steering while a turn runs and
// the "❯ " prompt after it.
type Chat struct {
	loops      bootstrap.Loops
	first      *loop.Loop // built by NewChat, so a bad setting fails before any call
	messages   []*schema.Message
	prompted   bool                 // --user-prompt gave the first turn its prompt
	sinks      []EventSink          // every event goes to each: the screen, then the recording if any
	recording  *bootstrap.Recording // nil: not recording
	input      *stdin.Lines
	steering   steering.Terminal
	prompt     prompt.Terminal
	interrupts *interrupt.Watcher
	stderr     io.Writer
}

// NewChat builds a Chat from options. Command.Run has already checked that
// stdin is a terminal.
func NewChat(ctx context.Context, env cli.Environment, opts bootstrap.Options) (*Chat, error) {

	// A person is at the terminal to answer ask_user and confirm questions.
	loops, err := bootstrap.NewLoops(ctx, env, opts, true)
	if err != nil {
		return nil, err
	}

	first, err := loops.New()
	if err != nil {
		return nil, err
	}

	messages, err := bootstrap.InitialMessages(opts)
	if err != nil {
		return nil, err
	}

	lines := stdin.NewLines(env.Stdin(), env.Stderr())

	sinks := []EventSink{render.NewText(env.Stderr(), env.Stdout(), loops.StopCondition())}

	// Recording: a second sink, opened last so no earlier failure leaves an
	// empty file behind. One file holds every turn of the session.
	var recording *bootstrap.Recording
	if opts.OutputRecordPath != "" {
		if recording, err = bootstrap.OpenRecording(opts.OutputRecordPath, time.Now()); err != nil {
			return nil, err
		}
		sinks = append(sinks, recording)
	}

	return &Chat{
		loops:      loops,
		first:      first,
		messages:   messages,
		prompted:   strings.TrimSpace(opts.UserPrompt) != "",
		sinks:      sinks,
		recording:  recording,
		input:      lines,
		steering:   steering.NewTerminal(lines.C(), env.Stderr()),
		prompt:     prompt.NewTerminal(lines.C(), env.Stderr()),
		interrupts: interrupt.NewWatcher(env.Stderr()),
		stderr:     env.Stderr(),
	}, nil
}

// Execute runs the session: a prompt, then a turn, again and again, with the
// conversation carried into a fresh loop each time. However a turn ends
// (an answer, /stop, Ctrl-C, max iterations, an error) the next prompt
// follows. The session ends at end of input (exit 0), on Ctrl-C at the
// prompt or a second Ctrl-C during a turn (exit 130).
//
// Two goroutines live for the whole session, both started here:
//
//  1. stdin lines  — c.input.Read; blocks on stdin, so it can outlive the
//     session (the process exits right after, so that is harmless)
//  2. Ctrl-C       — c.interrupts.Watch; cancel (deferred) ends it
//
// turn starts the goroutines of each turn.
func (c *Chat) Execute(parent context.Context) int {

	ctx, cancel := context.WithCancel(parent)

	defer cancel()

	defer c.closeRecording()

	go c.input.Read(ctx)

	go c.interrupts.Watch(ctx, cancel)

	l, messages := c.first, c.messages
	for n := 0; ; n++ {
		if n > 0 || !c.prompted {
			text, ok := c.prompt.Next(ctx)
			if !ok {
				if c.interrupts.Aborted() { // Ctrl-C at the prompt
					return cli.ExitStopped
				}
				return cli.ExitOK
			}
			messages = append(messages, &schema.Message{Role: schema.User, Content: text})
		}

		c.turn(ctx, l, messages)

		if c.interrupts.Aborted() {
			return cli.ExitStopped
		}

		// The turn is over: carry the conversation into a fresh loop.
		messages = l.Messages()
		var err error
		if l, err = c.loops.New(); err != nil {
			fmt.Fprintf(c.stderr, "steerloop: %v\n", err)
			return cli.ExitError
		}
	}
}

// turn runs one loop to its end and shows every event. Two goroutines take
// part, both started here:
//
//  3. the loop itself — l.Start; it closes events when it is done
//  4. steering input  — c.steering.Steer; endTurn ends it. turn waits for
//     it, so a line typed after the answer goes to the prompt, not to a
//     finished loop.
func (c *Chat) turn(ctx context.Context, l *loop.Loop, messages []*schema.Message) {

	turnCtx, endTurn := context.WithCancel(ctx)

	c.interrupts.Track(l)
	defer c.interrupts.Track(nil)

	events := l.Start(ctx, messages)

	steered := make(chan struct{})
	go func() {
		defer close(steered)
		c.steering.Steer(turnCtx, l)
	}()

	// Ends when the loop closes the channel after its final Done event.
	for evt := range events {
		for _, sink := range c.sinks {
			sink.Write(evt)
		}
	}

	endTurn()
	<-steered
}

// closeRecording closes the recording, if any, and says where it is.
func (c *Chat) closeRecording() {
	if c.recording == nil {
		return
	}
	if err := c.recording.Close(); err != nil {
		fmt.Fprintf(c.stderr, "steerloop: record: %v\n", err)
		return
	}
	fmt.Fprintf(c.stderr, "recorded to %s\n", c.recording.Path())
}
