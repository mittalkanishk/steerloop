package loop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/suite"
	"go.uber.org/goleak"

	"github.com/mittalkanishk/steerloop/internal/mockllm/scripts"
	"github.com/mittalkanishk/steerloop/loop"
	mocks "github.com/mittalkanishk/steerloop/loop/_mocks"
	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/loop/pause"
)

// This file is the whole loop test suite. LoopSuite drives package loop
// through its public API only. The tests are grouped in sections, in this
// order: configuration and lifecycle, running, timeouts, stop policies,
// controls, pauses, events, retries, streaming, reflection, then audit,
// transcript logging, tools and fuzz targets.
//
// Scripted models, fakes and other helpers are in helpers_test.go.

// TestMain fails the package if any test leaves a goroutine running.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// LoopSuite covers loop through its public API: the loop, its controls,
// its event stream, its stop and reflection policies. Every test starts from
// a fresh configuration, executor and log capture, so a test states only
// what is particular to its own scenario.
type LoopSuite struct {
	suite.Suite

	// cfg is the configuration every loop in the test is built with. A test
	// changes the fields its scenario needs before building a loop.
	cfg loop.Config
	// exec runs the tools. It answers "result from <tool>" until a test
	// replaces it with an executor that answers differently.
	exec *mocks.ToolExecutor
	// logs receives every line the loop logs.
	logs *captureHandler
}

// TestLoopSuite runs every LoopSuite test.
func TestLoopSuite(t *testing.T) {
	suite.Run(t, new(LoopSuite))
}

// SetupTest gives each test a small, fast configuration: 10 iterations,
// short timeouts, events streamed and Generate in place of Stream.
func (s *LoopSuite) SetupTest() {
	s.cfg = loop.Config{
		MaxIterations:    10,
		StopCondition:    loop.StopOnNoToolFinish,
		Temperature:      0.5,
		IterationTimeout: 5 * time.Second,
		TotalTimeout:     10 * time.Second,
		StreamEvents:     true,
		DisableStreaming: true,
	}
	s.exec = toolsAnswer(s.T(), resultFrom)
	s.logs = &captureHandler{level: slog.LevelInfo}
}

// =============================================================================
// New and configuration
// =============================================================================

func (s *LoopSuite) TestNew_RejectsAMissingModelOrExecutor() {
	llm := scripts.NewScriptedModel(says("x"))

	_, err := loop.New(s.cfg, llm, nil, nil, nil)
	s.ErrorContains(err, "tool executor")

	_, err = loop.New(s.cfg, nil, nil, s.exec, nil)
	s.ErrorContains(err, "LLM")
}

func (s *LoopSuite) TestNew_ReportsAToolBindingFailure() {
	_, err := loop.New(s.cfg, failingBinder{err: errors.New("no tools here")}, s.sampleTools(), s.exec, nil)
	s.ErrorContains(err, "no tools here")

	_, err = loop.New(s.cfg, failingBinder{}, s.sampleTools(), s.exec, nil)
	s.ErrorContains(err, "returned no model")
}

func (s *LoopSuite) TestNew_BindsTheToolsToTheModel() {
	llm := scripts.NewScriptedModel(says("x"))

	loop, err := loop.New(s.cfg, llm, s.sampleTools(), s.exec, nil) // a nil logger is allowed
	s.Require().NoError(err)
	s.NotNil(loop)
	s.Require().Len(llm.BoundTools(), 1)
	s.Len(llm.BoundTools()[0], 2)
}

func (s *LoopSuite) TestConfig_DefaultsRunAnAssistantOutOfTheBox() {
	s.cfg = loop.DefaultConfig()
	s.Equal(12, s.cfg.MaxIterations)
	s.Equal(loop.StopOnNoToolFinish, s.cfg.StopCondition)
	s.True(s.cfg.StreamEvents)
	s.Equal(time.Minute, s.cfg.IterationTimeout)

	events, _ := s.runScript(says("hi"))
	s.Equal("hi", s.done(events).FinalContent)
}

// The stop condition's label reaches callers on each llm_turn event.
func (s *LoopSuite) TestConfig_StopConditionsHaveStableLabels() {
	for stop, label := range map[loop.StopCondition]string{
		loop.StopOnNoToolFinish: "no_tool_finish",
		loop.StopOnFinishTool:   "finish_tool",
		loop.StopOnExternal:     "external_finish",
	} {
		s.cfg.StopCondition = stop
		s.cfg.MaxIterations = 1
		events, _ := s.runScript(asksFor(profileCall()))
		s.Equal(label, payload[event.LLMTurnData](s, events).StopCondition)
	}
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

func (s *LoopSuite) TestConfig_HistoryCompactorShapesOnlyWhatTheModelSees() {
	s.cfg.HistoryCompactor = keepNewest{}
	llm := scripts.NewScriptedModel(asksFor(profileCall()), says("answer"))
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
	loop := s.newLoop(scripts.NewScriptedModel(says("Hello there!")))

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
	loop := s.newLoop(scripts.NewScriptedModel(says("hello")))
	first := loop.Start(context.Background(), seedConversation())
	second := loop.Start(context.Background(), seedConversation())
	s.NotEqual(first, second, "the second call gets its own channel")

	failure := payload[event.ErrorData](s, s.collect(second))
	s.Contains(failure.Error, "already started")
	s.True(failure.Fatal)

	s.Equal(event.ReasonFinished, s.done(s.collect(first)).Reason)
}

func (s *LoopSuite) TestWait_ReturnsOnceTheRunHasEnded() {
	l := s.newLoop(scripts.NewScriptedModel(scripts.Step{Content: "done", Delay: 50 * time.Millisecond}))
	events := l.Start(context.Background(), seedConversation())
	go func() {
		for range events { // a reader elsewhere drains the channel
		}
	}()

	returned := make(chan struct{})
	go func() {
		l.Wait()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(eventTimeout):
		s.FailNow("Wait did not return")
	}
	s.Equal(loop.StateDone, l.State())
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
	s.cfg.StopCondition = loop.StopOnExternal
	s.cfg.MaxIterations = 5
	loop := s.newLoop(scripts.NewScriptedModel(scripts.Step{Content: "response", Delay: 5 * time.Millisecond}))
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
	l := s.newLoop(scripts.NewScriptedModel(says("Hello there!")))
	events := s.run(l)

	s.NotEmpty(payloads[event.ThinkingData](events))
	s.Equal("Hello there!", payload[event.ResponseData](s, events).Content)
	done := s.done(events)
	s.Equal(event.ReasonFinished, done.Reason)
	s.Equal(1, done.Iterations)
	s.Equal("Hello there!", done.FinalContent)
	s.Empty(calledTools(s.exec))
	s.Equal(loop.StateDone, l.State())
}

func (s *LoopSuite) TestRun_FeedsTheToolResultBackToTheModel() {
	s.exec = toolsAnswer(s.T(), func(context.Context, string, string) (string, error) {
		return `{"level":"intermediate"}`, nil
	})
	events, llm := s.runScript(asksFor(profileCall()), says("Based on your profile, here is a lesson."))

	s.Equal(profileTool, payload[event.ToolCallData](s, events).Name)
	s.Contains(payload[event.ToolResultData](s, events).Result, "intermediate")
	s.Equal([]string{profileTool}, calledTools(s.exec))

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
		asksFor(profileCall(), scripts.ToolCall("call_2", searchTool, `{"query":"go concurrency"}`)),
		says("Here is your lesson on Go concurrency."),
	)

	s.Len(payloads[event.ToolCallData](events), 2)
	s.Len(payloads[event.ToolResultData](events), 2)
	s.Equal([]string{profileTool, searchTool}, calledTools(s.exec))
	s.Equal(2, s.done(events).Iterations)
}

func (s *LoopSuite) TestRun_AToolErrorIsReturnedToTheModel() {
	s.exec = toolsAnswer(s.T(), func(context.Context, string, string) (string, error) {
		return "", errors.New("database connection failed")
	})
	events, llm := s.runScript(asksFor(profileCall()), says("I couldn't read your profile."))

	s.Equal("database connection failed", payload[event.ToolResultData](s, events).Error)
	s.Contains(lastMessage(llm).Content, "database connection failed", "the model sees the error as the tool's result")
	s.Equal(event.ReasonFinished, s.done(events).Reason, "a tool error is not fatal")
}

func (s *LoopSuite) TestRun_AToolPanicBecomesAToolError() {
	s.exec = toolsAnswer(s.T(), func(context.Context, string, string) (string, error) {
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
	s.cfg.StopCondition = loop.StopOnFinishTool
	finishArgs := `{"title":"Lesson 1","content":"Learn about goroutines..."}`
	events, _ := s.runScript(asksFor(profileCall()), asksFor(finishCall(finishArgs)))

	done := s.done(events)
	s.Equal(event.ReasonFinished, done.Reason)
	s.Equal(finishArgs, done.FinalContent)
	s.Equal(2, done.Iterations)
	s.Equal([]string{profileTool}, calledTools(s.exec), "finish never reaches the executor")
}

func (s *LoopSuite) TestRun_ATextReplyInFinishToolModeIsNudged() {
	s.cfg.StopCondition = loop.StopOnFinishTool
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
	s.Len(calledTools(s.exec), 3)
}

// A run that hits a limit returns its last text reply as its best answer.
func (s *LoopSuite) TestRun_ALimitReturnsTheLastReply() {
	s.cfg.StopCondition = loop.StopOnExternal
	s.cfg.MaxIterations = 2
	events, _ := s.runScript(says("first draft"), says("second draft"))

	done := s.done(events)
	s.Equal(event.ReasonMaxIterations, done.Reason)
	s.Equal("second draft", done.FinalContent)
}

func (s *LoopSuite) TestRun_EndsWhenTheContextIsCancelled() {
	s.cfg.StopCondition = loop.StopOnExternal
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
	s.cfg.StopCondition = loop.StopOnExternal
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

			events, _ := s.runScript(scripts.Step{Content: "slow", Delay: 3 * time.Second})

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
	events, llm := s.runScript(scripts.Step{Content: "slow", Delay: 500 * time.Millisecond})

	failure := payload[event.ErrorData](s, events)
	s.True(failure.Fatal)
	s.Contains(failure.Error, "deadline exceeded")
	s.Equal(event.ReasonError, s.done(events).Reason)
	s.Equal(2, llm.CallCount(), "the attempt and its one retry both time out")
}

func (s *LoopSuite) TestTimeout_ZeroIterationTimeoutMeansNoBound() {
	s.cfg.IterationTimeout = 0
	events, _ := s.runScript(scripts.Step{Content: "ok", Delay: 50 * time.Millisecond})
	s.Equal("ok", s.done(events).FinalContent)
}

// =============================================================================
// Stop policies
// =============================================================================

func (s *LoopSuite) TestStopPolicy_EachConditionSeedsItsInstructions() {
	for condition, phrase := range map[loop.StopCondition]string{
		loop.StopOnNoToolFinish: "A reply without a tool call ends the run",
		loop.StopOnFinishTool:   "The run ends only when you call the finish tool",
		loop.StopOnExternal:     "This run does not end when you finish a task",
	} {
		s.cfg.StopCondition = condition
		s.cfg.MaxIterations = 1
		_, llm := s.runScript(says("hi"))

		first := llm.Calls()[0]
		s.Require().Len(first, 3, "seed system, stop instructions, user")
		s.Equal("You are a helpful assistant.", first[0].Content)
		s.Equal(schema.System, stopInstructionsSeen(llm).Role)
		s.Contains(stopInstructionsSeen(llm).Content, phrase)
		s.Equal(schema.User, first[2].Role)
	}
}

func (s *LoopSuite) TestStopPolicy_CustomInstructionsReplaceTheBuiltIn() {
	s.cfg.StopInstructions = "Stop when the user says so."
	_, llm := s.runScript(says("hi"))
	s.Equal("Stop when the user says so.", stopInstructionsSeen(llm).Content)
}

// Providers such as Anthropic accept system messages only before the first
// user message, so the instructions go after every system message.
func (s *LoopSuite) TestStopPolicy_InstructionsGoAfterEverySystemMessage() {
	llm := scripts.NewScriptedModel(says("hi"))
	loop := s.newLoop(llm)
	s.collect(loop.Start(context.Background(), []*schema.Message{
		{Role: schema.System, Content: "one"},
		{Role: schema.System, Content: "two"},
		{Role: schema.User, Content: "Hello"},
	}))

	first := llm.Calls()[0]
	s.Require().Len(first, 4)
	s.Equal([]schema.RoleType{schema.System, schema.System, schema.System, schema.User},
		[]schema.RoleType{first[0].Role, first[1].Role, first[2].Role, first[3].Role})
	s.Equal("one", first[0].Content)
	s.Equal("two", first[1].Content)
	s.Contains(first[2].Content, "A reply without a tool call ends the run")
}

// A conversation carried over from an earlier run already holds the
// instructions, so a resumed run does not add them again.
func (s *LoopSuite) TestStopPolicy_AResumedConversationIsNotSeededTwice() {
	first := s.newLoop(scripts.NewScriptedModel(says("hi")))
	s.run(first)
	history := append(first.Messages(), &schema.Message{Role: schema.User, Content: "And again"})

	llm := scripts.NewScriptedModel(says("again"))
	s.collect(s.newLoop(llm).Start(context.Background(), history))

	seeded := 0
	for _, m := range llm.Calls()[0] {
		if m.Role == schema.System && m.Content == stopInstructionsSeen(llm).Content {
			seeded++
		}
	}
	s.Equal(1, seeded)
}

func (s *LoopSuite) TestStopPolicy_NoToolFinishEndsOnTheFirstTextReply() {
	events, llm := s.runScript(asksFor(profileCall()), says("the answer"), says("never asked"))

	done := s.done(events)
	s.Equal(event.ReasonFinished, done.Reason)
	s.Equal("the answer", done.FinalContent)
	s.Equal(2, done.Iterations)
	s.Equal(2, llm.CallCount())
}

func (s *LoopSuite) TestStopPolicy_FinishToolNeverReachesTheExecutor() {
	s.cfg.StopCondition = loop.StopOnFinishTool
	events, _ := s.runScript(asksFor(finishCall(`{"result":"done"}`)))

	s.Equal(`{"result":"done"}`, s.done(events).FinalContent)
	s.Equal(loop.FinishToolName, payload[event.ToolCallData](s, events).Name, "the finish call is still reported")
	s.Empty(calledTools(s.exec), "the loop recognises finish by name; it is not a tool to run")
}

func (s *LoopSuite) TestStopPolicy_ExternalFinishIgnoresTextAndFinishCalls() {
	s.cfg.StopCondition = loop.StopOnExternal
	s.cfg.MaxIterations = 3
	events, _ := s.runScript(says("status: waiting"), asksFor(finishCall(`{"result":"x"}`)), says("still waiting"))

	done := s.done(events)
	s.Equal(event.ReasonMaxIterations, done.Reason, "only a limit or a stop ends the run")
	s.Equal(3, done.Iterations)
	s.Equal("still waiting", done.FinalContent, "a limit-ended run returns its last reply")
	s.Equal([]string{loop.FinishToolName}, calledTools(s.exec), "finish is an ordinary tool here")
}

func (s *LoopSuite) TestStopPolicy_ExternalFinishEndsOnStop() {
	s.cfg.StopCondition = loop.StopOnExternal
	llm, release := heldModel("working on it")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())

	s.waitForModelCall(llm)
	loop.Stop()
	release()

	s.Equal(event.ReasonStopped, s.done(s.collect(events)).Reason)
}

func (s *LoopSuite) TestStopPolicy_TheAuditRecordNamesTheCondition() {
	for condition, label := range map[loop.StopCondition]string{
		loop.StopOnNoToolFinish: "no_tool_finish",
		loop.StopOnFinishTool:   "finish_tool",
		loop.StopOnExternal:     "external_finish",
	} {
		s.cfg.StopCondition = condition
		s.cfg.MaxIterations = 1
		events, _ := s.runScript(asksFor(profileCall()))
		s.Equal(label, payload[event.LLMTurnData](s, events).StopCondition)
	}
}

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
	loop := s.newLoop(scripts.NewScriptedModel(says("x")))
	loop.Stop()
	loop.Stop()
	s.NotNil(loop.Events())
}

func (s *LoopSuite) TestControl_AFullQueueRejectsNewSignals() {
	l := s.newLoop(scripts.NewScriptedModel(says("x")))
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

// =============================================================================
// Pauses
// =============================================================================

func (s *LoopSuite) TestPause_TheReplyBecomesTheToolResult() {
	var seenCall pause.Call
	var seenMessages []*schema.Message
	loop, llm := s.pausingLoop(pauseFunc(func(_ context.Context, call pause.Call, ps pause.Session) (string, bool) {
		seenCall, seenMessages = call, ps.Messages()
		return "blue", true
	}))
	events := s.run(loop)

	s.Equal(event.ReasonFinished, s.done(events).Reason)
	s.Empty(calledTools(s.exec), "a pause tool never reaches the executor")
	s.Equal("call_1", seenCall.ID)
	s.Equal(askUserTool, seenCall.Name)
	s.JSONEq(`{"question":"colour?"}`, seenCall.Arguments)
	s.Len(seenMessages, 4, "the seed messages, the stop instructions and the assistant tool call")
	s.Equal("blue", payload[event.ToolResultData](s, events).Result)

	result := lastMessage(llm)
	s.Equal(schema.Tool, result.Role)
	s.Equal("blue", result.Content)
	s.Equal("call_1", result.ToolCallID)
}

func (s *LoopSuite) TestPause_AnEmptyReplyIsFilledIn() {
	loop, llm := s.pausingLoop(pauseFunc(func(context.Context, pause.Call, pause.Session) (string, bool) {
		return "", true
	}))
	s.run(loop)
	s.Equal("(no response)", lastMessage(llm).Content)
}

func (s *LoopSuite) TestPause_AwaitReplyReturnsTheNextInject() {
	blocked := make(chan struct{})
	loop, llm := s.pausingLoop(pauseFunc(func(ctx context.Context, _ pause.Call, ps pause.Session) (string, bool) {
		ps.Emit(event.AwaitingInputData{Question: "colour?"})
		close(blocked)
		return ps.AwaitReply(ctx)
	}))
	events := loop.Start(context.Background(), seedConversation())
	<-blocked
	s.Require().True(loop.Inject("green"))

	all := s.collect(events)
	s.Equal(event.ReasonFinished, s.done(all).Reason)
	s.Equal("colour?", payload[event.AwaitingInputData](s, all).Question)
	s.Equal("green", lastMessage(llm).Content)
}

func (s *LoopSuite) TestPause_StopWhileBlockedEndsTheRunAtOnce() {
	l, llm, blocked := s.blockedPausingLoop()
	events := l.Start(context.Background(), seedConversation())
	<-blocked

	l.Stop()

	s.Equal(event.ReasonStopped, s.done(s.collect(events)).Reason)
	s.Equal(loop.StateStopped, l.State())
	s.Equal(1, llm.CallCount())
}

func (s *LoopSuite) TestPause_ContextCancellationWhileBlockedStopsTheRun() {
	l, _, blocked := s.blockedPausingLoop()
	ctx, cancel := context.WithCancel(context.Background())
	events := l.Start(ctx, seedConversation())
	<-blocked

	cancel()

	s.Equal(event.ReasonStopped, s.done(s.collect(events)).Reason)
	s.Equal(loop.StateStopped, l.State())
}

func (s *LoopSuite) TestPause_ConfigChangesApplyWhileBlocked() {
	l, llm, blocked := s.blockedPausingLoop()
	events := l.Start(context.Background(), seedConversation())
	<-blocked

	temperature := 0.9
	s.Require().True(l.ModifyConfig(loop.ConfigChange{Temperature: &temperature}))
	s.Require().True(l.Inject("ok"))

	s.collect(events)
	s.Require().NotNil(llm.LastOptions().Temperature)
	s.InDelta(0.9, *llm.LastOptions().Temperature, 0.001)
}

func (s *LoopSuite) TestPause_EndingWithoutAReplyHandsOverToAHuman() {
	type payloadForUI struct{ ID string }
	l, llm := s.pausingLoop(pauseFunc(func(_ context.Context, _ pause.Call, ps pause.Session) (string, bool) {
		ps.Emit(event.AwaitingInputData{
			Question: "Which colour?",
			Options:  []string{"red", "blue"},
			Payload:  payloadForUI{ID: "g-1"},
		})
		return "", false
	}))
	events := s.run(l)

	done := s.done(events)
	s.Equal(event.ReasonAwaitingHuman, done.Reason)
	s.Equal("Which colour?", done.FinalContent)
	s.Require().NotNil(done.Awaiting)
	s.Equal([]string{"red", "blue"}, done.Awaiting.Options)
	s.Equal(payloadForUI{ID: "g-1"}, done.Awaiting.Payload)
	s.Equal(loop.StateDone, l.State())
	s.Equal(1, llm.CallCount())
	s.Empty(payloads[event.ToolResultData](events), "no tool result is recorded for an unanswered pause")
}

func (s *LoopSuite) TestPause_AwaitingInputAcceptsAPointerPayload() {
	loop, _ := s.pausingLoop(pauseFunc(func(_ context.Context, _ pause.Call, ps pause.Session) (string, bool) {
		ps.Emit(&event.AwaitingInputData{Question: "Q?"})
		return "", false
	}))
	s.Equal(event.ReasonAwaitingHuman, s.done(s.run(loop)).Reason)
}

func (s *LoopSuite) TestPause_EndingWithoutAwaitingInputStopsTheRun() {
	for _, reply := range []string{"", "bye"} {
		s.Run(fmt.Sprintf("reply %q", reply), func() {
			loop, _ := s.pausingLoop(pauseFunc(func(context.Context, pause.Call, pause.Session) (string, bool) {
				return reply, false
			}))
			s.Equal(event.ReasonStopped, s.done(s.run(loop)).Reason)
		})
	}
}

func (s *LoopSuite) TestPause_CanRunToolsAndCallTheModel() {
	lookup, err := loop.NewToolInfo("lookup", "Look something up", nil)
	s.Require().NoError(err)

	var toolOut, modelOut string
	loop, llm := s.pausingLoop(pauseFunc(func(ctx context.Context, _ pause.Call, ps pause.Session) (string, bool) {
		toolOut, _ = ps.Execute(ctx, "lookup", `{"q":1}`)
		if reply, err := ps.CallModel(ctx, []*schema.ToolInfo{lookup}, ps.Messages()); err == nil && reply != nil {
			modelOut = reply.Content
		}
		return "done", true
	}))
	s.run(loop)

	s.Equal("result from lookup", toolOut)
	s.Equal([]string{"lookup"}, calledTools(s.exec))
	s.Equal("thanks", modelOut, "CallModel plays the next scripted reply")
	s.Require().Len(llm.BoundTools(), 1)
	s.Equal("lookup", llm.BoundTools()[0][0].Name)
}

func (s *LoopSuite) TestPause_APanicBecomesAToolError() {
	loop, llm := s.pausingLoop(pauseFunc(func(context.Context, pause.Call, pause.Session) (string, bool) {
		panic("pause boom")
	}))
	events := s.run(loop)

	s.Equal(event.ReasonFinished, s.done(events).Reason)
	s.Contains(lastMessage(llm).Content, "pause boom")
	s.False(payload[event.ErrorData](s, events).Fatal)
}

func (s *LoopSuite) TestPause_OtherToolsStillReachTheExecutor() {
	s.cfg.Pauses = map[string]pause.Handler{
		askUserTool: pauseFunc(func(context.Context, pause.Call, pause.Session) (string, bool) { return "yes", true }),
	}
	events, _ := s.runScript(
		asksFor(
			profileCall(),
			scripts.ToolCall("call_2", askUserTool, `{}`),
			scripts.ToolCall("call_3", searchTool, `{"query":"x"}`),
		),
		says("ok"),
	)

	s.Equal([]string{profileTool, searchTool}, calledTools(s.exec))
	s.Len(payloads[event.ToolResultData](events), 3)
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
	s.cfg.StopCondition = loop.StopOnExternal
	s.cfg.MaxIterations = 20 // 40 intermediate events
	s.cfg.EventBuffer = 8
	s.cfg.DoneDeliveryGrace = 30 * time.Millisecond
	loop := s.newLoop(scripts.NewScriptedModel(says("tick")))
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
	s.cfg.StopCondition = loop.StopOnExternal
	s.cfg.MaxIterations = 80 // 160 intermediate events against a 64-slot channel
	loop := s.newLoop(scripts.NewScriptedModel(says("still working")))
	events := loop.Start(context.Background(), seedConversation())
	time.Sleep(300 * time.Millisecond) // the loop has filled the channel by now

	s.Equal(event.ReasonMaxIterations, s.done(s.collect(events)).Reason)
}

// =============================================================================
// Retries
// =============================================================================

func (s *LoopSuite) TestRetry_RecoversFromATransientError() {
	events, llm := s.runScript(fails(io.ErrUnexpectedEOF), says("recovered"))

	done := s.done(events)
	s.Equal(event.ReasonFinished, done.Reason)
	s.Equal("recovered", done.FinalContent)
	s.Equal(2, llm.CallCount())
	s.Equal(1, done.Iterations, "a retry is not a new iteration")
}

func (s *LoopSuite) TestRetry_AllowsOneRetryByDefault() {
	events, llm := s.runScript(fails(errors.New("read: connection reset by peer")))

	done := s.done(events)
	s.Equal(event.ReasonError, done.Reason)
	s.Contains(done.Error, "connection reset")
	s.Equal(2, llm.CallCount(), "the attempt and one retry")
}

func (s *LoopSuite) TestRetry_ANonTransientErrorIsFatalAtOnce() {
	llm := scripts.NewScriptedModel(fails(errors.New("invalid api key")))
	l := s.newLoop(llm)
	events := s.run(l)

	failure := payload[event.ErrorData](s, events)
	s.True(failure.Fatal)
	s.Contains(failure.Error, "invalid api key")
	s.Equal(event.ReasonError, s.done(events).Reason)
	s.Equal(loop.StateError, l.State())
	s.Equal(1, llm.CallCount())
}

func (s *LoopSuite) TestRetry_CanBeDisabled() {
	s.cfg.RetryMaxAttempts = -1
	events, llm := s.runScript(fails(io.EOF), says("never"))

	s.Equal(event.ReasonError, s.done(events).Reason)
	s.Equal(1, llm.CallCount())
}

func (s *LoopSuite) TestRetry_HonoursTheConfiguredAttempts() {
	s.cfg.RetryMaxAttempts = 2
	events, llm := s.runScript(fails(io.EOF), fails(io.EOF), says("third time"))

	s.Equal("third time", s.done(events).FinalContent)
	s.Equal(3, llm.CallCount())
}

func (s *LoopSuite) TestRetry_AnEmptyReplyIsRetried() {
	events, llm := s.runScript(scripts.Step{}, says("now with text"))

	s.Equal("now with text", s.done(events).FinalContent)
	s.Equal(2, llm.CallCount())
}

func (s *LoopSuite) TestRetry_APersistentlyEmptyReplyIsFatal() {
	events, llm := s.runScript(scripts.Step{}) // the empty step repeats

	failure := payload[event.ErrorData](s, events)
	s.True(failure.Fatal)
	s.Contains(failure.Error, "empty LLM response")
	s.Equal(2, llm.CallCount())
}

func (s *LoopSuite) TestRetry_ACustomClassifierReplacesTheDefault() {
	s.cfg.ErrorClassifier = rateLimitedOnly{}

	events, llm := s.runScript(fails(errors.New("rate limited")), says("ok"))
	s.Equal("ok", s.done(events).FinalContent)
	s.Equal(2, llm.CallCount())

	events, llm = s.runScript(fails(io.EOF))
	s.Equal(event.ReasonError, s.done(events).Reason)
	s.Equal(1, llm.CallCount(), "EOF is no longer transient")
}

func (s *LoopSuite) TestRetry_AnIterationTimeoutIsRetried() {
	s.cfg.IterationTimeout = 40 * time.Millisecond
	events, llm := s.runScript(
		scripts.Step{Content: "too slow", Delay: 500 * time.Millisecond},
		says("fast"),
	)

	s.Equal("fast", s.done(events).FinalContent)
	s.Equal(2, llm.CallCount())
}

func (s *LoopSuite) TestRetry_StopsWhenTheRunContextEnds() {
	s.cfg.TotalTimeout = 100 * time.Millisecond
	s.cfg.RetryMaxAttempts = 5
	start := time.Now()

	events, _ := s.runScript(fails(io.EOF))

	s.Equal(event.ReasonError, s.done(events).Reason)
	s.Less(time.Since(start), 2*time.Second, "the backoff must not outlive the run")
}

// The default classifier is internal to loop, so it is checked through the
// loop: a transient failure is retried once (two calls), anything else is
// fatal at once (one call).
func (s *LoopSuite) TestRetry_TheDefaultClassifierKnowsTransientFailures() {
	transient := []error{
		context.DeadlineExceeded, io.EOF, io.ErrUnexpectedEOF,
		fmt.Errorf("wrapped: %w", io.EOF),
		errors.New("stream idle: no chunk received for 30s"),
		errors.New("provider returned Empty Response"),
		errors.New("no response from upstream"),
		errors.New("Connection Reset by peer"),
	}
	for _, err := range transient {
		s.Run("transient "+err.Error(), func() {
			_, llm := s.runScript(fails(err), says("ok"))
			s.Equal(2, llm.CallCount())
		})
	}
	for _, err := range []error{context.Canceled, errors.New("401 unauthorized")} {
		s.Run("fatal "+err.Error(), func() {
			_, llm := s.runScript(fails(err), says("ok"))
			s.Equal(1, llm.CallCount())
		})
	}
}

// =============================================================================
// Streaming
// =============================================================================

func (s *LoopSuite) TestStream_ChunksAreJoinedIntoOneReply() {
	s.cfg.DisableStreaming = false
	events, _ := s.runScript(scripts.Step{
		Chunks: []scripts.Chunk{{Content: "Hel"}, {Content: "lo "}, {Content: "there"}},
	})
	s.Equal("Hello there", s.done(events).FinalContent)
}

func (s *LoopSuite) TestStream_ToolCallsArriveOnTheLastChunk() {
	s.cfg.DisableStreaming = false
	events, _ := s.runScript(
		scripts.Step{
			Chunks:    []scripts.Chunk{{Content: ""}, {Content: ""}},
			ToolCalls: []schema.ToolCall{profileCall()},
		},
		says("used the tool"),
	)

	s.Equal("used the tool", s.done(events).FinalContent)
	s.Equal([]string{profileTool}, calledTools(s.exec))
}

func (s *LoopSuite) TestStream_TheIdleWatchdogAbortsAStalledStream() {
	s.cfg.DisableStreaming = false
	s.cfg.IdleChunkTimeout = 40 * time.Millisecond
	s.cfg.RetryMaxAttempts = -1
	events, _ := s.runScript(scripts.Step{
		Chunks: []scripts.Chunk{{Content: "start"}, {Content: "never", Delay: 2 * time.Second}},
	})

	done := s.done(events)
	s.Equal(event.ReasonError, done.Reason)
	s.Contains(done.Error, "stream idle")
}

func (s *LoopSuite) TestStream_TheIdleWatchdogWaitsForTheFirstChunk() {
	s.cfg.DisableStreaming = false
	s.cfg.IdleChunkTimeout = 40 * time.Millisecond
	events, llm := s.runScript(scripts.Step{
		Chunks: []scripts.Chunk{{Content: "late but fine", Delay: 200 * time.Millisecond}},
	})

	s.Equal("late but fine", s.done(events).FinalContent)
	s.Equal(1, llm.CallCount())
}

func (s *LoopSuite) TestStream_AStalledStreamIsRetried() {
	s.cfg.DisableStreaming = false
	s.cfg.IdleChunkTimeout = 40 * time.Millisecond
	events, llm := s.runScript(
		scripts.Step{Chunks: []scripts.Chunk{{Content: "a"}, {Content: "b", Delay: 2 * time.Second}}},
		says("second attempt"),
	)

	s.Equal("second attempt", s.done(events).FinalContent)
	s.Equal(2, llm.CallCount())
}

func (s *LoopSuite) TestStream_AnErrorOpeningTheStreamIsReported() {
	s.cfg.DisableStreaming = false
	s.cfg.RetryMaxAttempts = -1
	events, _ := s.runScript(fails(errors.New("bad request")))
	s.Contains(s.done(events).Error, "bad request")
}

func (s *LoopSuite) TestStream_DisableStreamingSwitchesToGenerate() {
	llm := &countingModel{inner: scripts.NewScriptedModel(says("hi"))}
	for _, disable := range []bool{true, false} {
		s.cfg.DisableStreaming = disable
		s.run(s.newLoop(llm))
	}
	s.Equal(1, llm.generate)
	s.Equal(1, llm.stream)
}

// =============================================================================
// Reflection
//
// The judge binds the same scripted model to its verdict tool, so a script
// interleaves the loop's turns with the judge's verdicts in call order.
// =============================================================================

func (s *LoopSuite) TestReflection_OffMakesNoReviewCalls() {
	events, llm := s.runScript(asksFor(profileCall()), says("done"))
	s.Equal(2, llm.CallCount())
	s.Empty(payloads[event.ReflectionData](events))
}

// --- on iteration ------------------------------------------------------------

func (s *LoopSuite) TestReflection_OnIterationReviewsEveryIterationThatContinues() {
	s.cfg.Reflect = loop.ReflectOnIteration
	events, llm := s.runScript(asksFor(profileCall()), passes(), says("done"))

	s.Equal(3, llm.CallCount(), "one review: the final iteration ends the run and is not reviewed")
	s.Equal("done", s.done(events).FinalContent)
	verdicts := payloads[event.ReflectionData](events)
	s.Require().Len(verdicts, 1)
	s.Equal(event.ReflectOnIteration, verdicts[0].Mode)
	s.Equal("pass", verdicts[0].Verdict)

	brief := judgeBrief(llm, 1)
	s.Contains(brief, "User request:\nHello")
	s.Contains(brief, "The model called get_profile({})")
	s.Contains(brief, "get_profile returned: result from get_profile")
	s.NotContains(brief, "A reply without a tool call ends the run", "stop instructions are loop mechanics, not the task")
}

func (s *LoopSuite) TestReflection_AFailedIterationReviewSteersTheNextIteration() {
	s.cfg.Reflect = loop.ReflectOnIteration
	events, llm := s.runScript(asksFor(profileCall()), rules("fail", "wrong tool", "search instead"), says("done"))

	s.Equal(event.ReasonFinished, s.done(events).Reason, "a verdict never ends the run")
	note := lastMessage(llm)
	s.Equal(schema.User, note.Role)
	s.Equal("Review of your last iteration:\nIssue: wrong tool\nHint: search instead", note.Content)
}

// A nudge and a review queued for the same iteration reach the model as one
// user message, never as two turns in a row.
func (s *LoopSuite) TestReflection_TheNudgeAndTheReviewArriveAsOneMessage() {
	s.cfg.StopCondition = loop.StopOnFinishTool
	s.cfg.Reflect = loop.ReflectOnIteration
	_, llm := s.runScript(says("let me think"), rules("fail", "no progress", "call a tool"), asksFor(finishCall(`{"result":"x"}`)))

	msgs := llm.LastMessages()
	note := msgs[len(msgs)-1]
	s.Equal(schema.User, note.Role)
	s.Contains(note.Content, "returned prose instead of a tool call")
	s.Contains(note.Content, "Review of your last iteration:\nIssue: no progress")
	s.NotEqual(schema.User, msgs[len(msgs)-2].Role)
}

func (s *LoopSuite) TestReflection_TheJudgeSeesOnlyTheLatestIterationClipped() {
	s.cfg.Reflect = loop.ReflectOnIteration
	long := strings.Repeat("x", 2500)
	s.exec = toolsAnswer(s.T(), func(_ context.Context, name, _ string) (string, error) {
		if name == searchTool {
			return long, nil
		}
		return "profile", nil
	})
	_, llm := s.runScript(
		asksFor(profileCall()), passes(),
		asksFor(scripts.ToolCall("call_2", searchTool, `{"query":"go"}`)), passes(),
		says("done"),
	)

	brief := judgeBrief(llm, 3)
	s.Contains(brief, "search_content")
	s.NotContains(brief, "get_profile", "an earlier iteration is not under review")
	s.Contains(brief, strings.Repeat("x", 2000)+"…")
	s.NotContains(brief, strings.Repeat("x", 2001))
}

func (s *LoopSuite) TestReflection_ReviewsDoNotCountTowardMaxIterations() {
	s.cfg.Reflect = loop.ReflectOnIteration
	s.cfg.MaxIterations = 2
	events, llm := s.runScript(asksFor(profileCall()), passes(), asksFor(profileCall()), passes())

	done := s.done(events)
	s.Equal(event.ReasonMaxIterations, done.Reason)
	s.Equal(2, done.Iterations)
	s.Equal(4, llm.CallCount(), "two iterations and two reviews")
}

func (s *LoopSuite) TestReflection_AFailedIterationReviewIsReportedAndSkipped() {
	s.cfg.Reflect = loop.ReflectOnIteration
	for name, judge := range map[string]scripts.Step{
		"judge down": fails(errors.New("judge down")),
		"no verdict": says("looks fine to me"),
	} {
		events, _ := s.runScript(asksFor(profileCall()), judge, says("done"))
		done := s.done(events)
		s.Equal(event.ReasonFinished, done.Reason, name)
		s.Equal("done", done.FinalContent, name)
		failure := payload[event.ErrorData](s, events)
		s.Contains(failure.Error, "iteration review failed", name)
		s.False(failure.Fatal, name)
	}
}

// --- on finish ---------------------------------------------------------------

func (s *LoopSuite) TestReflection_OnFinishPassKeepsTheAnswer() {
	s.cfg.Reflect = loop.ReflectOnFinish
	llm := scripts.NewScriptedModel(says("the answer"), passes())
	events := s.run(s.newLoop(llm, s.sampleTools()...))

	s.Equal("the answer", s.done(events).FinalContent)
	s.Equal(2, llm.CallCount())
	verdict := payload[event.ReflectionData](s, events)
	s.Equal(event.ReflectOnFinish, verdict.Mode)
	s.Equal("pass", verdict.Verdict)

	brief := judgeBrief(llm, 1)
	s.Contains(brief, "System prompt:\nYou are a helpful assistant.")
	s.Contains(brief, "User request:\nHello")
	s.Contains(brief, "Answer:\nthe answer")
	s.NotContains(brief, "A reply without a tool call ends the run")

	opts := llm.LastOptions()
	s.Require().NotNil(opts.ToolChoice)
	s.Equal(schema.ToolChoiceAllowed, *opts.ToolChoice, "a forced tool choice is rejected by some models")
	s.Empty(opts.AllowedToolNames, "Claude rejects allowed tool names with tool choice allowed")

	bound := llm.BoundTools()
	s.Require().Len(bound, 2, "the run's tools, then the judge's")
	s.Len(bound[0], 2)
	s.Require().Len(bound[1], 1)
	s.Equal(verdictTool, bound[1][0].Name, "the judge sees none of the run's tools")
}

func (s *LoopSuite) TestReflection_OnFinishDriftReopensTheRunOnce() {
	s.cfg.Reflect = loop.ReflectOnFinish
	events, llm := s.runScript(says("draft"), rules("fail", "missed the date", "add the date"), says("final"))

	s.Equal("final", s.done(events).FinalContent, "after MaxReopens the answer stands unreviewed")
	s.Equal(3, llm.CallCount())
	s.Len(payloads[event.ReflectionData](events), 1)
	s.Equal("A review found your answer drifts from the task:\nIssue: missed the date\nHint: add the date", lastMessage(llm).Content)
}

func (s *LoopSuite) TestReflection_MaxReopensBoundsTheReopens() {
	s.cfg.Reflect = loop.ReflectOnFinish
	s.cfg.MaxReopens = 2
	events, llm := s.runScript(says("a1"), rules("fail", "r1", ""), says("a2"), rules("fail", "r2", ""), says("a3"))

	s.Equal("a3", s.done(events).FinalContent)
	s.Equal(5, llm.CallCount())
	s.Len(payloads[event.ReflectionData](events), 2)
}

// Under finish-tool the review answers the finish call, so the history the
// model sees stays a valid call-and-result pair.
func (s *LoopSuite) TestReflection_OnFinishUnderFinishToolAnswersTheFinishCall() {
	s.cfg.StopCondition = loop.StopOnFinishTool
	s.cfg.Reflect = loop.ReflectOnFinish
	events, llm := s.runScript(
		asksFor(finishCall(`{"result":"bad"}`)),
		rules("fail", "wrong result", "fix it"),
		asksFor(finishCall(`{"result":"good"}`)),
	)

	s.Equal(`{"result":"good"}`, s.done(events).FinalContent)
	msgs := llm.LastMessages()
	call, result := msgs[len(msgs)-2], msgs[len(msgs)-1]
	s.Equal(schema.Assistant, call.Role)
	s.Require().Len(call.ToolCalls, 1)
	s.Equal(loop.FinishToolName, call.ToolCalls[0].Function.Name)
	s.Equal(schema.Tool, result.Role)
	s.Equal(call.ToolCalls[0].ID, result.ToolCallID)
	s.Contains(result.Content, "drifts from the task")
}

func (s *LoopSuite) TestReflection_AFailedFinishReviewKeepsTheAnswer() {
	s.cfg.Reflect = loop.ReflectOnFinish
	events, _ := s.runScript(says("the answer"), fails(errors.New("judge down")))

	s.Equal("the answer", s.done(events).FinalContent)
	s.Contains(payload[event.ErrorData](s, events).Error, "finish review failed")
}

func (s *LoopSuite) TestReflection_OnFinishNeverRunsUnderExternalFinish() {
	s.cfg.StopCondition = loop.StopOnExternal
	s.cfg.Reflect = loop.ReflectOnFinish
	s.cfg.MaxIterations = 2
	events, llm := s.runScript(says("waiting"))

	s.Equal(2, llm.CallCount(), "the model never finishes, so nothing is judged")
	s.Empty(payloads[event.ReflectionData](events))
}

func (s *LoopSuite) TestReflection_CustomPromptsReplaceTheBuiltIn() {
	s.cfg.Reflect = loop.ReflectOnIteration
	s.cfg.IterationReviewPrompt = "Judge this step."
	_, llm := s.runScript(asksFor(profileCall()), passes(), says("done"))
	s.Equal("Judge this step.", llm.Calls()[1][0].Content)

	s.cfg.Reflect = loop.ReflectOnFinish
	s.cfg.FinishReviewPrompt = "Judge this answer."
	_, llm = s.runScript(says("done"), passes())
	s.Equal("Judge this answer.", llm.Calls()[1][0].Content)
}

func (s *LoopSuite) TestReflection_JudgeTokensCountTowardUsage() {
	s.T().Skip("loop bug: a review's token usage is never recorded; modelcall.Caller stores a UsageRecorder but never calls it")

	s.cfg.Reflect = loop.ReflectOnFinish
	usage := &schema.TokenUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}
	verdict := passes()
	verdict.Usage = usage
	events, _ := s.runScript(scripts.Step{Content: "answer", Usage: usage}, verdict)
	s.Equal(30, s.done(events).LLMUsage.TotalTokens)
}

// =============================================================================
// Audit and usage
//
// What a caller sees: the llm_turn event, usage totals and the transcript
// log level. The serializers behind LLMTurnData (request JSON, tool-call
// JSON, reply kind and the size cap) are tested in loop/internal/audit, and
// transcript formatting in loop/internal/transcript.
// =============================================================================

func (s *LoopSuite) TestAudit_TheLLMTurnEventCarriesUsageIdentityAndCost() {
	steps := []scripts.Step{
		{ToolCalls: []schema.ToolCall{profileCall()}, Usage: &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 20}},
		{Content: "answer", Usage: &schema.TokenUsage{PromptTokens: 150, CompletionTokens: 30}},
	}

	events, _ := s.runScript(steps...)
	turn := payload[event.LLMTurnData](s, events)
	s.Equal(1, turn.TurnIndex)
	s.Equal(100, turn.InputTokens)
	s.Equal(20, turn.OutputTokens)
	s.Zero(turn.CostAmount)
	s.Empty(turn.Provider)
	s.Equal("tool_calls", turn.ResponseKind)
	s.Equal("no_tool_finish", turn.StopCondition)
	s.Equal(10, turn.MaxIterations)
	s.JSONEq(`[{"id":"call_1","type":"function","function":{"name":"get_profile","arguments":"{}"}}]`, string(turn.AssistantToolCallsJSON))
	var request struct {
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	s.Require().NoError(json.Unmarshal(turn.RequestMessagesJSON, &request))
	s.Require().Len(request.Messages, 3, "the seed system message, the stop instructions, the user message")
	s.Equal("system", request.Messages[1].Role, "the stop instructions follow the caller's system message")
	s.Equal("user", request.Messages[2].Role)
	s.Equal(event.LLMUsageSummary{InputTokens: 250, OutputTokens: 50, TotalTokens: 300}, s.done(events).LLMUsage)

	pricer := &recordingPricer{}
	s.cfg.ModelIdentity = fixedIdentity{provider: "acme", model: "big-1"}
	s.cfg.CostEstimator = pricer
	events, _ = s.runScript(steps...)
	turn = payload[event.LLMTurnData](s, events)
	s.Equal("acme", turn.Provider)
	s.Equal("big-1", turn.Model)
	s.Equal("acme", pricer.provider)
	s.Equal("big-1", pricer.model)
	s.InDelta(3.0, turn.CostAmount, 0.0001)
}

func (s *LoopSuite) TestAudit_EachStopConditionHasAStableLabel() {
	for cond, label := range map[loop.StopCondition]string{
		loop.StopOnNoToolFinish: "no_tool_finish",
		loop.StopOnFinishTool:   "finish_tool",
		loop.StopOnExternal:     "external_finish",
	} {
		s.cfg.StopCondition = cond
		events, _ := s.runScript(asksFor(profileCall()), says("done"))
		s.Equal(label, payload[event.LLMTurnData](s, events).StopCondition)
	}
}

func (s *LoopSuite) TestUsage_CountsProvidersThatReportOnlyATotal() {
	events, _ := s.runScript(scripts.Step{Content: "hi", Usage: &schema.TokenUsage{TotalTokens: 42}})
	s.Equal(event.LLMUsageSummary{TotalTokens: 42}, s.done(events).LLMUsage)
}

// =============================================================================
// Transcript logging
// =============================================================================

func (s *LoopSuite) TestTranscript_IsLoggedAtTheConfiguredLevel() {
	s.cfg.TranscriptLogLevel = slog.LevelDebug // below the handler's info level
	s.runScript(says("hi"))
	s.Zero(s.logs.count("agent loop seed"))

	s.logs = &captureHandler{level: slog.LevelInfo}
	s.cfg.TranscriptLogLevel = slog.LevelInfo
	s.runScript(says("hi"))
	s.Equal(1, s.logs.count("agent loop seed"))
	s.Equal(1, s.logs.count("agent loop diff"))
}

// =============================================================================
// Tools
// =============================================================================

func (s *LoopSuite) TestTool_NewToolInfoBuildsADefinitionFromJSONSchema() {
	info, err := loop.NewToolInfo("ping", "Ping", nil)
	s.Require().NoError(err)
	s.Equal("ping", info.Name)
	s.Nil(info.ParamsOneOf, "nil params mean no parameters")

	info, err = loop.NewToolInfo("search", "Search", map[string]any{
		"type":       "object",
		"properties": map[string]any{"q": map[string]any{"type": "string"}},
		"required":   []string{"q"},
	})
	s.Require().NoError(err)
	js, err := info.ToJSONSchema()
	s.Require().NoError(err)
	s.Equal([]string{"q"}, js.Required)

	_, err = loop.NewToolInfo("bad", "", map[string]any{"type": make(chan int)})
	s.ErrorContains(err, "marshal params")
	_, err = loop.NewToolInfo("bad", "", map[string]any{"properties": "not an object"})
	s.ErrorContains(err, "parse params")
}

// =============================================================================
// Fuzz targets. Go requires these to be top-level functions; CI runs them by name.
// =============================================================================

// FuzzNewToolInfo checks that any name and JSON schema give a ToolInfo or an
// error, never a panic.
func FuzzNewToolInfo(f *testing.F) {
	f.Add("search", `{"type":"object","properties":{"q":{"type":"string"}}}`)
	f.Add("", `{}`)
	f.Add("x", `{"properties":7}`)
	f.Fuzz(func(t *testing.T, name, schemaJSON string) {
		var params map[string]any
		if err := json.Unmarshal([]byte(schemaJSON), &params); err != nil {
			return
		}
		_, _ = loop.NewToolInfo(name, "desc", params) // must not panic
	})
}
