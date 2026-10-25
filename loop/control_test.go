package loop_test

import (
	"context"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/loop/pause"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// controlQueueSize is the capacity of the loop's control queue
// (loop/internal/control.queueSize), which tests outside the package
// cannot read.
const controlQueueSize = 8

// =============================================================================
// Controls: stop, inject and modify config
// =============================================================================

func (s *LoopSuite) TestStop_EndsTheRunAtTheNextIterationBoundary() {
	s.cfg.StopCondition = loop.StopOnExternal
	llm, release := heldModel("tick")
	l := s.newLoop(llm)
	events := l.Start(context.Background(), seedConversation())
	s.waitForModelCall(llm)

	l.Stop()
	release()

	s.Equal(event.ReasonStopped, s.done(s.collect(events)).Reason)
	s.Equal(loop.StateStopped, l.State())
	s.Equal(1, llm.CallCount(), "the call in flight completes; no new call starts")
}

func (s *LoopSuite) TestStop_CannotBeLostWhenTheControlQueueIsFull() {
	s.cfg.StopCondition = loop.StopOnExternal
	llm, release := heldModel("working")
	l := s.newLoop(llm)
	events := l.Start(context.Background(), seedConversation())
	s.waitForModelCall(llm)

	temperature := 0.1
	for range controlQueueSize {
		s.Require().True(l.ModifyConfig(loop.ConfigChange{Temperature: &temperature}))
	}
	s.False(l.ModifyConfig(loop.ConfigChange{Temperature: &temperature}), "the queue is full")
	l.Stop()
	release()

	s.Equal(event.ReasonStopped, s.done(s.collect(events)).Reason)
	s.Equal(loop.StateStopped, l.State())
	s.Equal(1, llm.CallCount(), "no model call starts after Stop")
}

func (s *LoopSuite) TestStop_IsIdempotent() {
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("x")))
	loop.Stop()
	loop.Stop()
	s.NotNil(loop.Events())
}

func (s *LoopSuite) TestControl_AFullQueueRejectsNewSignals() {
	l := s.newLoop(steerlooptest.NewScriptedModel(says("x")))
	for range controlQueueSize {
		s.Require().True(l.Inject("m"))
	}
	s.False(l.Inject("one too many"))
	five := 5
	s.False(l.ModifyConfig(loop.ConfigChange{MaxIterations: &five}))
}

func (s *LoopSuite) TestControl_IgnoresEmptyInjects() {
	s.cfg.StopCondition = loop.StopOnExternal
	s.cfg.MaxIterations = 2
	llm, release := heldModel("tick")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())

	s.Require().True(loop.Inject(""))
	release()

	all := s.collect(events)
	s.Empty(payloads[event.ProgressData](all))
	s.Len(loop.Messages(), 5, "seed, stop instructions and two replies; nothing was injected")
}

func (s *LoopSuite) TestInject_AddsAUserMessageBeforeTheNextModelCall() {
	s.cfg.StopCondition = loop.StopOnExternal
	s.cfg.MaxIterations = 2
	llm, release := heldModel("tick")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())
	s.waitForModelCall(llm)

	s.Require().True(loop.Inject("use Python examples"))
	release()

	all := s.collect(events)
	s.Equal(event.ReasonMaxIterations, s.done(all).Reason)
	s.Equal("inject", payload[event.ProgressData](s, all).Step)

	calls := llm.Calls()
	s.Require().Len(calls, 2)
	s.Len(calls[0], 3, "the call in flight is unaffected: seed and stop instructions")
	injected := calls[1][len(calls[1])-1]
	s.Equal(schema.User, injected.Role)
	s.Equal("use Python examples", injected.Content)
}

func (s *LoopSuite) TestModifyConfig_RaisesTheIterationCapAndChangesTheTemperature() {
	s.cfg.StopCondition = loop.StopOnExternal
	s.cfg.MaxIterations = 2
	llm, release := heldModel("tick")
	l := s.newLoop(llm)
	events := l.Start(context.Background(), seedConversation())

	five, temperature := 5, 0.1
	s.Require().True(l.ModifyConfig(loop.ConfigChange{MaxIterations: &five, Temperature: &temperature}))
	release()

	all := s.collect(events)
	done := s.done(all)
	s.Equal(event.ReasonMaxIterations, done.Reason)
	s.Equal(5, done.Iterations)
	s.Require().NotNil(llm.LastOptions().Temperature)
	s.InDelta(0.1, *llm.LastOptions().Temperature, 0.001, "later calls use the new temperature")

	progress := payloads[event.ProgressData](all)
	s.Require().Len(progress, 1)
	s.Equal("config_modify", progress[0].Step)
}

func (s *LoopSuite) TestModifyConfig_DuringAPauseIsRaceFree() {
	// Configuration changes arrive while the pause makes model calls from
	// another goroutine. Run with -race.
	blocked := make(chan struct{})
	l, _ := s.pausingLoop(pauseFunc(func(ctx context.Context, _ pause.Call, ps pause.Session) (string, bool) {
		var sideCalls sync.WaitGroup
		sideCalls.Add(1)
		go func() {
			defer sideCalls.Done()
			for range 50 {
				_, _ = ps.CallModel(ctx, nil, ps.Messages())
				ps.Emit(event.ProgressData{Message: "side"})
			}
		}()
		close(blocked)
		reply, ok := ps.AwaitReply(ctx)
		sideCalls.Wait()
		return reply, ok
	}))
	events := l.Start(context.Background(), seedConversation())
	go func() {
		for range events { // keep the channel drained
		}
	}()

	<-blocked
	for i := range 50 {
		temperature := float64(i) / 100
		for !l.ModifyConfig(loop.ConfigChange{Temperature: &temperature}) {
			time.Sleep(time.Millisecond)
		}
		_ = l.State()
	}
	for !l.Inject("the answer") {
		time.Sleep(time.Millisecond)
	}
	l.Wait()

	done, ok := l.Result()
	s.Require().True(ok)
	s.Equal(event.ReasonFinished, done.Reason)
}
