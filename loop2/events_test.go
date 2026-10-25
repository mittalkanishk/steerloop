package loop2_test

import (
	"context"
	"sync"
	"time"

	"github.com/mittalkanishk/steerloop/loop2"
	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// typeRecorder is an EventSink that keeps the type of every event it sees.
type typeRecorder struct {
	mu    sync.Mutex
	types []event.Type
}

func (r *typeRecorder) Write(evt event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types = append(r.types, evt.Type)
}

func (r *typeRecorder) seen() []event.Type {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event.Type(nil), r.types...)
}

// =============================================================================
// Events
// =============================================================================

func (s *LoopSuite) TestEvents_TheSinkSeesEveryEventInOrder() {
	sink := &typeRecorder{}
	s.cfg.EventSink = sink
	events, _ := s.runScript(asksFor(profileCall()), says("done"))

	var fromChannel []event.Type
	for _, evt := range events {
		fromChannel = append(fromChannel, evt.Type)
	}
	s.Equal(fromChannel, sink.seen())
	s.Equal([]event.Type{
		event.Thinking, event.LLMTurn, event.ToolCall, event.ToolResult,
		event.Thinking, event.Response, event.Done,
	}, sink.seen())
}

func (s *LoopSuite) TestEvents_StreamEventsOffEmitsOnlyTheOutcome() {
	sink := &typeRecorder{}
	s.cfg.StreamEvents = false
	s.cfg.EventSink = sink
	events, _ := s.runScript(asksFor(profileCall()), says("Here is the lesson."))

	s.Require().Len(events, 1)
	s.Equal(event.Done, events[0].Type)
	s.Equal([]event.Type{event.Done}, sink.seen())
}

func (s *LoopSuite) TestEvents_CarryATimestampAndTheirIteration() {
	before := time.Now()
	events, _ := s.runScript(asksFor(profileCall()), says("Response after tools."))
	after := time.Now()

	for _, evt := range events {
		s.False(evt.Timestamp.Before(before), "%s is stamped before the run", evt.Type)
		s.False(evt.Timestamp.After(after), "%s is stamped after the run", evt.Type)
	}
	thinking := eventsOfType(events, event.Thinking)
	s.Require().Len(thinking, 2)
	s.Equal(1, thinking[0].Iteration)
	s.Equal(2, thinking[1].Iteration)
	dones := eventsOfType(events, event.Done)
	s.Require().Len(dones, 1)
	s.Equal(2, dones[0].Iteration)
}

func (s *LoopSuite) TestEvents_AnUnreadLoopDropsAndCountsThem() {
	s.cfg.StopCondition = loop2.StopOnExternal
	s.cfg.MaxIterations = 20 // 40 intermediate events
	s.cfg.EventBuffer = 8
	s.cfg.DoneDeliveryGrace = 30 * time.Millisecond
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("tick")))
	loop.Start(context.Background(), seedConversation())
	loop.Wait()

	done, ok := loop.Result()
	s.Require().True(ok)
	s.Equal(event.ReasonMaxIterations, done.Reason)
	s.Equal(32, done.DroppedEvents)
	s.Equal(32, s.logs.count("agent loop event channel full, dropping event"))
	s.Equal(1, s.logs.count("agent loop done event not read within grace period; use Result"))
}

func (s *LoopSuite) TestEvents_DoneReachesAReaderThatStartsLate() {
	s.cfg.StopCondition = loop2.StopOnExternal
	s.cfg.MaxIterations = 80 // 160 intermediate events against a 64-slot channel
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("still working")))
	events := loop.Start(context.Background(), seedConversation())
	time.Sleep(300 * time.Millisecond) // the loop has filled the channel by now

	s.Equal(event.ReasonMaxIterations, s.done(s.collect(events)).Reason)
}
