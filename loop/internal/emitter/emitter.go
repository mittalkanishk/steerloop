// Package emitter owns the event channel: it filters, counts dropped events,
// and guarantees the done event is sent once and waited for.
package emitter

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mittalkanishk/steerloop/loop/event"
)

// Sink receives every event, synchronously, before it is offered to the
// channel. → the caller's loop.EventSink, or defaults.NoSink
type Sink interface {
	Write(evt event.Event)
}

// Iterations says which iteration an event belongs to. → state.Run
type Iterations interface {
	Iteration() int
}

// Results records the run's outcome before the done event is sent. → state.Run
type Results interface {
	SetResult(d event.DoneData)
}

// Options are the emitter's fixed settings.
type Options struct {
	Buffer       int
	StreamEvents bool          // false: only Error and Done events are emitted
	DoneGrace    time.Duration // how long Done waits for a reader when the channel is full
}

type Emitter struct {
	mu       sync.RWMutex // orders sends on ch against its close
	ch       chan event.Event
	closed   bool
	dropped  atomic.Int64
	doneOnce sync.Once

	opts       Options
	sink       Sink
	iterations Iterations
	results    Results
	logger     *slog.Logger
}

func New(opts Options, sink Sink, iterations Iterations, results Results, logger *slog.Logger) *Emitter {
	return &Emitter{
		ch:         make(chan event.Event, opts.Buffer),
		opts:       opts,
		sink:       sink,
		iterations: iterations,
		results:    results,
		logger:     logger,
	}
}

// Events is the channel the caller reads. Close closes it.
func (e *Emitter) Events() <-chan event.Event {
	return e.ch
}

// Dropped counts events dropped because the channel was full.
func (e *Emitter) Dropped() int {
	return int(e.dropped.Load())
}

// Emit offers an intermediate event. When the channel is full the event is
// dropped and counted.
func (e *Emitter) Emit(data event.Data) {
	t := data.Type()
	if !e.opts.StreamEvents && t != event.Done && t != event.Error {
		return
	}
	evt := event.New(data, e.iterations.Iteration())

	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.closed {
		return
	}
	e.sink.Write(evt)

	select {
	case e.ch <- evt:
	default:
		e.dropped.Add(1)
		e.logger.Warn("agent loop event channel full, dropping event",
			slog.String("type", string(t)), slog.Int("iteration", evt.Iteration))
	}
}

// Done records the outcome and sends the done event. Only the first call has
// any effect, so a run has exactly one outcome. Unlike Emit, a full channel
// does not drop it: the send waits for a reader for up to DoneGrace.
func (e *Emitter) Done(d event.DoneData) {
	e.doneOnce.Do(func() {
		e.results.SetResult(d)
		evt := event.New(d, d.Iterations)

		e.mu.RLock()
		defer e.mu.RUnlock()
		if e.closed {
			return
		}
		e.sink.Write(evt)

		select {
		case e.ch <- evt:
			return
		default:
		}

		timer := time.NewTimer(e.opts.DoneGrace)
		defer timer.Stop()
		select {
		case e.ch <- evt:
		case <-timer.C:
			e.logger.Warn("agent loop done event not read within grace period; use Result")
		}
	})
}

// Close closes the channel. Events emitted afterwards are discarded.
func (e *Emitter) Close() {
	e.mu.Lock()
	e.closed = true
	close(e.ch)
	e.mu.Unlock()
}
