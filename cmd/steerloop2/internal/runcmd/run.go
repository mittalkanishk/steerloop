package runcmd

import (
	"context"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cli"
	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/interrupt"
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
	loop       *loop2.Loop
	messages   []*schema.Message
	sink       EventSink
	steering   SteeringSource
	interrupts *interrupt.Watcher
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

	events := r.loop.Start(ctx, r.messages)
	go r.steering.Steer(ctx, r.loop)
	go r.interrupts.Watch(ctx, cancel, r.loop)

	// Ends when the loop closes the channel after its final Done event.
	for evt := range events {
		r.sink.Write(evt)
	}

	if r.interrupts.Aborted() {
		return cli.ExitStopped
	}
	result, _ := r.loop.Result()
	return cli.ExitCodeFor(result.Reason)
}
