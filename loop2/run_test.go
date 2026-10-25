package loop2_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2"
	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// =============================================================================
// New and configuration
// =============================================================================

func (s *LoopSuite) TestNew_RejectsAMissingModelOrExecutor() {
	llm := steerlooptest.NewScriptedModel(says("x"))

	_, err := loop2.New(s.cfg, llm, nil, nil, nil)
	s.ErrorContains(err, "tool executor")

	_, err = loop2.New(s.cfg, nil, nil, s.exec, nil)
	s.ErrorContains(err, "LLM")
}

func (s *LoopSuite) TestNew_ReportsAToolBindingFailure() {
	_, err := loop2.New(s.cfg, failingBinder{err: errors.New("no tools here")}, s.sampleTools(), s.exec, nil)
	s.ErrorContains(err, "no tools here")

	_, err = loop2.New(s.cfg, failingBinder{}, s.sampleTools(), s.exec, nil)
	s.ErrorContains(err, "returned no model")
}

func (s *LoopSuite) TestNew_BindsTheToolsToTheModel() {
	llm := steerlooptest.NewScriptedModel(says("x"))

	loop, err := loop2.New(s.cfg, llm, s.sampleTools(), s.exec, nil) // a nil logger is allowed
	s.Require().NoError(err)
	s.NotNil(loop)
	s.Require().Len(llm.BoundTools(), 1)
	s.Len(llm.BoundTools()[0], 2)
}

func (s *LoopSuite) TestConfig_DefaultsRunAnAssistantOutOfTheBox() {
	s.cfg = loop2.DefaultConfig()
	s.Equal(12, s.cfg.MaxIterations)
	s.Equal(loop2.StopOnNoToolFinish, s.cfg.StopCondition)
	s.True(s.cfg.StreamEvents)
	s.Equal(time.Minute, s.cfg.IterationTimeout)

	events, _ := s.runScript(says("hi"))
	s.Equal("hi", s.done(events).FinalContent)
}

// The stop condition's label reaches callers on each llm_turn event.
func (s *LoopSuite) TestConfig_StopConditionsHaveStableLabels() {
	for stop, label := range map[loop2.StopCondition]string{
		loop2.StopOnNoToolFinish: "no_tool_finish",
		loop2.StopOnFinishTool:   "finish_tool",
		loop2.StopOnExternal:     "external_finish",
	} {
		s.cfg.StopCondition = stop
		s.cfg.MaxIterations = 1
		events, _ := s.runScript(asksFor(profileCall()))
		s.Equal(label, payload[event.LLMTurnData](s, events).StopCondition)
	}
}

// maxTokensOptions is a CallOptionsBuilder that records the temperature it
// was given and sends only a max-tokens option.
type maxTokensOptions struct {
	temperatureSeen float32
}

func (o *maxTokensOptions) Build(temperature float32) []model.Option {
	o.temperatureSeen = temperature
	return []model.Option{model.WithMaxTokens(77)}
}

func (s *LoopSuite) TestConfig_CallOptionsReplaceTheDefaultOptions() {
	s.cfg.Temperature = 0.3
	_, llm := s.runScript(says("hi"))
	s.Require().NotNil(llm.LastOptions().Temperature)
	s.InDelta(0.3, *llm.LastOptions().Temperature, 0.001, "the default is model.WithTemperature")

	options := &maxTokensOptions{}
	s.cfg.CallOptions = options
	_, llm = s.runScript(says("hi"))
	s.InDelta(0.3, options.temperatureSeen, 0.001)
	s.Require().NotNil(llm.LastOptions().MaxTokens)
	s.Equal(77, *llm.LastOptions().MaxTokens)
	s.Nil(llm.LastOptions().Temperature, "the builder replaces the default options")
}

// keepNewest is a HistoryCompactor that keeps only the newest message.
type keepNewest struct{}

func (keepNewest) Compact(msgs []*schema.Message) []*schema.Message {
	return msgs[len(msgs)-1:]
}

func (s *LoopSuite) TestConfig_HistoryCompactorShapesOnlyWhatTheModelSees() {
	s.cfg.HistoryCompactor = keepNewest{}
	llm := steerlooptest.NewScriptedModel(asksFor(profileCall()), says("answer"))
	loop := s.newLoop(llm)
	s.run(loop)

	for _, call := range llm.Calls() {
		s.Len(call, 1, "the model sees the compacted history")
	}
	s.Equal(schema.Tool, llm.LastMessages()[0].Role)
	// system, stop instructions, user, tool call, tool result, answer
	s.Len(loop.Messages(), 6, "the stored history is not compacted")
}

// =============================================================================
// Start, Wait, Result and State
// =============================================================================

func (s *LoopSuite) TestStart_LeavesTheCallerMessagesUntouched() {
	seed := seedConversation()
	first := seed[0]
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("Hello there!")))

	s.collect(loop.Start(context.Background(), seed))

	s.Len(seed, 2, "the caller's slice is not appended to")
	s.Same(first, seed[0])

	running := loop.Messages()
	s.Require().Len(running, 4, "the working copy grows: seed, stop instructions, reply")
	s.Same(first, running[0])
	s.Equal(schema.System, running[1].Role, "the stop instructions follow the caller's system message")
	s.Equal(schema.User, running[2].Role)
	s.Equal(schema.Assistant, running[3].Role)
	s.Equal("Hello there!", running[3].Content)
}

func (s *LoopSuite) TestStart_ASecondCallFailsWithoutDisturbingTheRun() {
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("hello")))
	first := loop.Start(context.Background(), seedConversation())
	second := loop.Start(context.Background(), seedConversation())
	s.NotEqual(first, second, "the second call gets its own channel")

	failure := payload[event.ErrorData](s, s.collect(second))
	s.Contains(failure.Error, "already started")
	s.True(failure.Fatal)

	s.Equal(event.ReasonFinished, s.done(s.collect(first)).Reason)
}

func (s *LoopSuite) TestWait_ReturnsOnceTheRunHasEnded() {
	loop := s.newLoop(steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "done", Delay: 50 * time.Millisecond}))
	events := loop.Start(context.Background(), seedConversation())
	go func() {
		for range events { // a reader elsewhere drains the channel
		}
	}()

	returned := make(chan struct{})
	go func() {
		loop.Wait()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(eventTimeout):
		s.FailNow("Wait did not return")
	}
	s.Equal(loop2.StateDone, loop.State())
}

func (s *LoopSuite) TestResult_IsAvailableOnlyAfterTheRunEnds() {
	llm, release := heldModel("final")
	loop := s.newLoop(llm)
	_, ok := loop.Result()
	s.False(ok, "no result before Start")

	events := loop.Start(context.Background(), seedConversation())
	_, ok = loop.Result()
	s.False(ok, "no result while running")

	release()
	s.collect(events)
	done, ok := loop.Result()
	s.Require().True(ok)
	s.Equal(event.ReasonFinished, done.Reason)
	s.Equal("final", done.FinalContent)
	s.Equal(1, done.Iterations)
}

func (s *LoopSuite) TestState_AccessorsAreSafeForConcurrentUse() {
	s.cfg.StopCondition = loop2.StopOnExternal
	s.cfg.MaxIterations = 5
	loop := s.newLoop(steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "response", Delay: 5 * time.Millisecond}))
	events := loop.Start(context.Background(), seedConversation())

	var readers sync.WaitGroup
	for range 20 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			_ = loop.State()
			_ = loop.Iteration()
			_ = loop.Messages()
		}()
	}
	readers.Wait()

	s.Equal(event.ReasonMaxIterations, s.done(s.collect(events)).Reason)
}

// =============================================================================
// Running: replies, tools and stop conditions
// =============================================================================

func (s *LoopSuite) TestRun_ATextReplyEndsTheRun() {
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("Hello there!")))
	events := s.run(loop)

	s.NotEmpty(payloads[event.ThinkingData](events))
	s.Equal("Hello there!", payload[event.ResponseData](s, events).Content)
	done := s.done(events)
	s.Equal(event.ReasonFinished, done.Reason)
	s.Equal(1, done.Iterations)
	s.Equal("Hello there!", done.FinalContent)
	s.Zero(s.exec.CallCount())
	s.Equal(loop2.StateDone, loop.State())
}

func (s *LoopSuite) TestRun_FeedsTheToolResultBackToTheModel() {
	s.exec = steerlooptest.NewRecordingExecutor(func(context.Context, string, string) (string, error) {
		return `{"level":"intermediate"}`, nil
	})
	events, llm := s.runScript(asksFor(profileCall()), says("Based on your profile, here is a lesson."))

	s.Equal(profileTool, payload[event.ToolCallData](s, events).Name)
	s.Contains(payload[event.ToolResultData](s, events).Result, "intermediate")
	s.Equal([]string{profileTool}, s.exec.CalledTools())

	s.Equal(2, llm.CallCount())
	result := lastMessage(llm)
	s.Equal(schema.Tool, result.Role, "the second call ends with the tool result")
	s.Equal("call_1", result.ToolCallID)
	s.Equal(profileTool, result.ToolName)
	s.Contains(result.Content, "intermediate")

	done := s.done(events)
	s.Equal(event.ReasonFinished, done.Reason)
	s.Equal(2, done.Iterations)
}

func (s *LoopSuite) TestRun_ExecutesEveryToolCallOfATurn() {
	events, _ := s.runScript(
		asksFor(profileCall(), steerlooptest.ToolCall("call_2", searchTool, `{"query":"go concurrency"}`)),
		says("Here is your lesson on Go concurrency."),
	)

	s.Len(payloads[event.ToolCallData](events), 2)
	s.Len(payloads[event.ToolResultData](events), 2)
	s.Equal([]string{profileTool, searchTool}, s.exec.CalledTools())
	s.Equal(2, s.done(events).Iterations)
}

func (s *LoopSuite) TestRun_AToolErrorIsReturnedToTheModel() {
	s.exec = steerlooptest.NewRecordingExecutor(func(context.Context, string, string) (string, error) {
		return "", errors.New("database connection failed")
	})
	events, llm := s.runScript(asksFor(profileCall()), says("I couldn't read your profile."))

	s.Equal("database connection failed", payload[event.ToolResultData](s, events).Error)
	s.Contains(lastMessage(llm).Content, "database connection failed", "the model sees the error as the tool's result")
	s.Equal(event.ReasonFinished, s.done(events).Reason, "a tool error is not fatal")
}

func (s *LoopSuite) TestRun_AToolPanicBecomesAToolError() {
	s.exec = steerlooptest.NewRecordingExecutor(func(context.Context, string, string) (string, error) {
		panic("boom")
	})
	events, _ := s.runScript(asksFor(profileCall()), says("recovered"))

	result := payload[event.ToolResultData](s, events)
	s.Contains(result.Error, "panicked")
	s.Contains(result.Result, "boom")
	s.Equal(event.ReasonFinished, s.done(events).Reason)

	failures := payloads[event.ErrorData](events)
	s.Require().Len(failures, 1)
	s.False(failures[0].Fatal)
}

func (s *LoopSuite) TestRun_TheFinishToolEndsTheRunWithItsArguments() {
	s.cfg.StopCondition = loop2.StopOnFinishTool
	finishArgs := `{"title":"Lesson 1","content":"Learn about goroutines..."}`
	events, _ := s.runScript(asksFor(profileCall()), asksFor(finishCall(finishArgs)))

	done := s.done(events)
	s.Equal(event.ReasonFinished, done.Reason)
	s.Equal(finishArgs, done.FinalContent)
	s.Equal(2, done.Iterations)
	s.Equal([]string{profileTool}, s.exec.CalledTools(), "finish never reaches the executor")
}

func (s *LoopSuite) TestRun_ATextReplyInFinishToolModeIsNudged() {
	s.cfg.StopCondition = loop2.StopOnFinishTool
	finish := asksFor(finishCall(`{"ok":true}`))

	events, llm := s.runScript(says("thinking aloud"), finish)
	done := s.done(events)
	s.Equal(event.ReasonFinished, done.Reason)
	s.Equal(`{"ok":true}`, done.FinalContent)
	s.Equal(2, done.Iterations)
	nudge := lastMessage(llm)
	s.Equal(schema.User, nudge.Role)
	s.Contains(nudge.Content, "returned prose instead of a tool call", "the built-in nudge follows the text reply")

	s.cfg.AutoNudgeText = "Use a tool."
	_, llm = s.runScript(says("thinking aloud"), finish)
	s.Equal("Use a tool.", lastMessage(llm).Content)

	s.cfg.DisableAutoNudge = true
	_, llm = s.runScript(says("thinking aloud"), finish)
	s.Equal("thinking aloud", lastMessage(llm).Content, "no synthetic user message is added")
}

func (s *LoopSuite) TestRun_StopsAtMaxIterations() {
	s.cfg.MaxIterations = 3
	events, _ := s.runScript(asksFor(profileCall())) // the model never stops asking for the tool

	done := s.done(events)
	s.Equal(event.ReasonMaxIterations, done.Reason)
	s.Equal(3, done.Iterations)
	s.Equal(3, s.exec.CallCount())
}

// A run that hits a limit returns its last text reply as its best answer.
func (s *LoopSuite) TestRun_ALimitReturnsTheLastReply() {
	s.cfg.StopCondition = loop2.StopOnExternal
	s.cfg.MaxIterations = 2
	events, _ := s.runScript(says("first draft"), says("second draft"))

	done := s.done(events)
	s.Equal(event.ReasonMaxIterations, done.Reason)
	s.Equal("second draft", done.FinalContent)
}

func (s *LoopSuite) TestRun_EndsWhenTheContextIsCancelled() {
	s.cfg.StopCondition = loop2.StopOnExternal
	llm, release := heldModel("response")
	loop := s.newLoop(llm)
	ctx, cancel := context.WithCancel(context.Background())
	events := loop.Start(ctx, seedConversation())
	s.waitForModelCall(llm)

	cancel()
	release()

	s.Equal(event.ReasonTimeout, s.done(s.collect(events)).Reason)
	s.Equal(1, llm.CallCount(), "no model call starts after the cancellation")
}

// =============================================================================
// Timeouts
// =============================================================================

func (s *LoopSuite) TestTimeout_TotalTimeoutEndsTheRunAtTheNextBoundary() {
	s.cfg.StopCondition = loop2.StopOnExternal
	s.cfg.TotalTimeout = 50 * time.Millisecond
	llm, release := heldModel("response")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())
	s.waitForModelCall(llm)

	time.Sleep(100 * time.Millisecond) // the deadline passes while the call is held
	release()

	s.Equal(event.ReasonTimeout, s.done(s.collect(events)).Reason)
	s.Equal(1, llm.CallCount())
}

func (s *LoopSuite) TestTimeout_TotalTimeoutReachesAModelCallInFlight() {
	// With decoupling, the default, the call is cancelled. Without it the
	// call's own deadline is the remaining total time, so it fails with a
	// deadline error.
	for _, tc := range []struct {
		name    string
		disable bool
		want    string
	}{
		{"decoupled", false, "context canceled"},
		{"coupled", true, "deadline exceeded"},
	} {
		s.Run(tc.name, func() {
			s.cfg.TotalTimeout = 60 * time.Millisecond
			s.cfg.IterationTimeout = 5 * time.Second
			s.cfg.DisableIterationTimeoutDecoupling = tc.disable
			start := time.Now()

			events, _ := s.runScript(steerlooptest.Step{Content: "slow", Delay: 3 * time.Second})

			done := s.done(events)
			s.Equal(event.ReasonError, done.Reason)
			s.Contains(done.Error, tc.want)
			s.Less(time.Since(start), 2*time.Second)
		})
	}
}

func (s *LoopSuite) TestTimeout_IterationTimeoutFailsTheRunOnceRetriesAreSpent() {
	s.cfg.IterationTimeout = 50 * time.Millisecond
	s.cfg.TotalTimeout = 2 * time.Second
	events, llm := s.runScript(steerlooptest.Step{Content: "slow", Delay: 500 * time.Millisecond})

	failure := payload[event.ErrorData](s, events)
	s.True(failure.Fatal)
	s.Contains(failure.Error, "deadline exceeded")
	s.Equal(event.ReasonError, s.done(events).Reason)
	s.Equal(2, llm.CallCount(), "the attempt and its one retry both time out")
}

func (s *LoopSuite) TestTimeout_ZeroIterationTimeoutMeansNoBound() {
	s.cfg.IterationTimeout = 0
	events, _ := s.runScript(steerlooptest.Step{Content: "ok", Delay: 50 * time.Millisecond})
	s.Equal("ok", s.done(events).FinalContent)
}
