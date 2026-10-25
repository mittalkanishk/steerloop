package engine

import (
	"log/slog"

	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/loop/internal/emitter"
	"github.com/mittalkanishk/steerloop/loop/internal/state"
)

// Finisher ends a run. Every way a run can end goes through it, and the
// emitter delivers only the first done event, so a run has one outcome.
// It also implements dispatch.Ender.
type Finisher struct {
	state   *state.Run
	emitter *emitter.Emitter
	logger  *slog.Logger
	closed  chan struct{}
}

func NewFinisher(st *state.Run, em *emitter.Emitter, logger *slog.Logger) *Finisher {
	return &Finisher{
		state:   st,
		emitter: em,
		logger:  logger,
		closed:  make(chan struct{}),
	}
}

// Done ends the run for reason.
func (f *Finisher) Done(reason event.DoneReason, finalContent string) {
	f.deliver(event.DoneData{Reason: reason, FinalContent: finalContent})
}

// Fail ends the run on a fatal error.
func (f *Finisher) Fail(msg string) {
	f.state.Fail(msg)
	f.emitter.Emit(event.ErrorData{Error: msg, Fatal: true})
	f.Done(event.ReasonError, "")
}

// EndStopped ends the run because it was told to stop.
func (f *Finisher) EndStopped() {
	f.logger.Info("agent loop received stop signal")
	f.state.SetStatus(state.Stopped)
	f.Done(event.ReasonStopped, "")
}

// EndAwaitingHuman ends the run so a person can answer later.
func (f *Finisher) EndAwaitingHuman(awaiting event.AwaitingInputData) {
	f.deliver(event.DoneData{Reason: event.ReasonAwaitingHuman, FinalContent: awaiting.Question, Awaiting: &awaiting})
}

// Close runs when Engine.Run returns, whatever path ended it: it makes sure
// a done event exists, settles the status and closes the event channel.
func (f *Finisher) Close() {
	f.Done(event.ReasonStopped, "") // no effect if the run already has an outcome
	f.state.MarkDoneIfRunning()
	f.emitter.Close()
	close(f.closed)
}

// Wait blocks until Close has run.
func (f *Finisher) Wait() {
	<-f.closed
}

func (f *Finisher) deliver(d event.DoneData) {
	d.Iterations = f.state.Iteration()
	d.LLMUsage = f.state.Usage().Summary()
	d.DroppedEvents = f.emitter.Dropped()
	if d.Reason == event.ReasonError {
		d.Error = f.state.LastError()
	}
	f.emitter.Done(d)
}
