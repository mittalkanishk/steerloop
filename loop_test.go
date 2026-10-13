package steerloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/suite"
	"go.uber.org/goleak"

	"github.com/mittalkanishk/steerloop/steerlooptest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// LoopSuite covers the loop, its controls, its event stream and the test
// doubles in steerlooptest. Every test starts from a fresh configuration,
// executor and log capture, so a test states only what is particular to its
// own scenario.
type LoopSuite struct {
	suite.Suite

	// cfg is the configuration every loop in the test is built with. A test
	// changes the fields its scenario needs before building a loop.
	cfg LoopConfig
	// exec runs the tools. It answers "result from <tool>" until a test
	// replaces it with an executor that answers differently.
	exec *steerlooptest.RecordingExecutor
	// logs receives every line the loop logs.
	logs *captureHandler
}

func TestLoopSuite(t *testing.T) {
	suite.Run(t, new(LoopSuite))
}

func (s *LoopSuite) SetupTest() {
	s.cfg = LoopConfig{
		MaxIterations:    10,
		StopCondition:    StopOnNoToolCalls,
		Temperature:      0.5,
		StepTimeout:      5 * time.Second,
		TotalTimeout:     10 * time.Second,
		StreamEvents:     true,
		DisableStreaming: true,
	}
	s.exec = steerlooptest.NewRecordingExecutor(nil)
	s.logs = &captureHandler{level: slog.LevelInfo}
}

// =============================================================================
// Fixtures
// =============================================================================

const (
	// eventTimeout bounds how long a test waits for a run to end.
	eventTimeout = 15 * time.Second

	profileTool = "get_profile"
	searchTool  = "search_content"
	askUserTool = "ask_user"
)

// seedConversation is the conversation every run starts from.
func seedConversation() []*schema.Message {
	return []*schema.Message{
		{Role: schema.System, Content: "You are a helpful assistant."},
		{Role: schema.User, Content: "Hello"},
	}
}

// says is a model turn that answers with text.
func says(text string) steerlooptest.Step {
	return steerlooptest.Step{Content: text}
}

// asksFor is a model turn that asks for tools.
func asksFor(toolCalls ...schema.ToolCall) steerlooptest.Step {
	return steerlooptest.Step{ToolCalls: toolCalls}
}

// fails is a model turn whose call returns err.
func fails(err error) steerlooptest.Step {
	return steerlooptest.Step{Err: err}
}

// profileCall is a call to the profile tool.
func profileCall() schema.ToolCall {
	return steerlooptest.ToolCall("call_1", profileTool, `{}`)
}

// finishCall ends a StopOnFinishTool run with args as its result.
func finishCall(args string) schema.ToolCall {
	return steerlooptest.ToolCall("call_finish", FinishToolName, args)
}

// sampleTools are the tool definitions a test binds to the model.
func (s *LoopSuite) sampleTools() []*schema.ToolInfo {
	profile, err := NewToolInfo(profileTool, "Get the user profile", map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	})
	s.Require().NoError(err)

	search, err := NewToolInfo(searchTool, "Search stored content", map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
		"required":   []string{"query"},
	})
	s.Require().NoError(err)

	return []*schema.ToolInfo{profile, search}
}

// newLoop builds a loop from the suite's configuration, executor and logs.
func (s *LoopSuite) newLoop(llm model.ToolCallingChatModel, tools ...*schema.ToolInfo) *Loop {
	s.T().Helper()
	loop, err := New(s.cfg, llm, tools, s.exec, slog.New(s.logs))
	s.Require().NoError(err)
	return loop
}

// run starts loop from the seed conversation and returns everything it emits.
func (s *LoopSuite) run(loop *Loop) []Event {
	s.T().Helper()
	return s.collect(loop.Start(context.Background(), seedConversation()))
}

// runScript runs a loop against a model that plays steps in order.
func (s *LoopSuite) runScript(steps ...steerlooptest.Step) ([]Event, *steerlooptest.ScriptedModel) {
	s.T().Helper()
	llm := steerlooptest.NewScriptedModel(steps...)
	return s.run(s.newLoop(llm)), llm
}

// collect reads events until the channel is closed.
func (s *LoopSuite) collect(events <-chan Event) []Event {
	s.T().Helper()
	timeout := time.NewTimer(eventTimeout)
	defer timeout.Stop()

	var all []Event
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return all
			}
			all = append(all, evt)
		case <-timeout.C:
			s.Require().FailNowf("the run did not end", "collected %d events", len(all))
		}
	}
}

// done returns the payload of the run's single done event.
func (s *LoopSuite) done(events []Event) DoneData {
	s.T().Helper()
	dones := eventsOfType(events, EventDone)
	s.Require().Len(dones, 1, "a run has exactly one done event")
	return dones[0].Data.(DoneData)
}

// payload returns the data of the first event of type t.
func payload[T any](s *LoopSuite, events []Event, t EventType) T {
	s.T().Helper()
	evt := firstEvent(events, t)
	s.Require().NotNil(evt, "no %s event was emitted", t)
	data, ok := evt.Data.(T)
	s.Require().True(ok, "%s event carries %T", t, evt.Data)
	return data
}

func firstEvent(events []Event, t EventType) *Event {
	for i := range events {
		if events[i].Type == t {
			return &events[i]
		}
	}
	return nil
}

func eventsOfType(events []Event, t EventType) []Event {
	var matched []Event
	for _, evt := range events {
		if evt.Type == t {
			matched = append(matched, evt)
		}
	}
	return matched
}

// lastMessage is the newest message the model saw on its most recent call.
func lastMessage(llm *steerlooptest.ScriptedModel) *schema.Message {
	msgs := llm.LastMessages()
	return msgs[len(msgs)-1]
}

// heldModel answers every call with text but holds its first call open until
// release is called. A test uses it to act on a loop that is known to be
// inside a model call.
func heldModel(text string) (llm *steerlooptest.ScriptedModel, release func()) {
	gate := make(chan struct{})
	var first, once sync.Once
	llm = &steerlooptest.ScriptedModel{
		Handler: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			first.Do(func() { <-gate })
			return &schema.Message{Role: schema.Assistant, Content: text}, nil
		},
	}
	return llm, func() { once.Do(func() { close(gate) }) }
}

// waitForModelCall returns once the loop is inside its first model call.
func (s *LoopSuite) waitForModelCall(llm *steerlooptest.ScriptedModel) {
	s.T().Helper()
	s.Require().Eventually(func() bool { return llm.CallCount() > 0 }, eventTimeout, time.Millisecond)
}

// pausingLoop builds a loop whose first model turn calls the ask_user pause
// tool and whose second turn says "thanks".
func (s *LoopSuite) pausingLoop(pause PauseFunc) (*Loop, *steerlooptest.ScriptedModel) {
	s.T().Helper()
	llm := steerlooptest.NewScriptedModel(
		asksFor(steerlooptest.ToolCall("call_1", askUserTool, `{"question":"colour?"}`)),
		says("thanks"),
	)
	s.cfg.Pauses = map[string]PauseFunc{askUserTool: pause}
	return s.newLoop(llm), llm
}

// blockedPausingLoop builds a pausing loop whose pause waits for an inject.
// blocked is closed once the pause is waiting.
func (s *LoopSuite) blockedPausingLoop() (loop *Loop, llm *steerlooptest.ScriptedModel, blocked <-chan struct{}) {
	s.T().Helper()
	waiting := make(chan struct{})
	loop, llm = s.pausingLoop(func(ctx context.Context, in PauseIn) (string, bool) {
		close(waiting)
		return in.Block(ctx)
	})
	return loop, llm, waiting
}

// failingBinder is a model whose WithTools fails, or returns no model.
type failingBinder struct{ err error }

func (failingBinder) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, nil
}

func (failingBinder) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (f failingBinder) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return nil, f.err
}

// countingModel counts how often each model entry point is used.
type countingModel struct {
	inner            *steerlooptest.ScriptedModel
	generate, stream int
}

func (c *countingModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	c.generate++
	return c.inner.Generate(ctx, in, opts...)
}

func (c *countingModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	c.stream++
	return c.inner.Stream(ctx, in, opts...)
}

func (c *countingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return c, nil
}

// captureHandler is a slog.Handler that keeps every record for assertions.
type captureHandler struct {
	level   slog.Level
	mu      sync.Mutex
	entries []logEntry
}

type logEntry struct {
	msg   string
	attrs map[string]any
}

func (h *captureHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	entry := logEntry{msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		entry.attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.entries = append(h.entries, entry)
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) all() []logEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]logEntry(nil), h.entries...)
}

// count returns how many records carry msg.
func (h *captureHandler) count(msg string) int {
	n := 0
	for _, entry := range h.all() {
		if entry.msg == msg {
			n++
		}
	}
	return n
}

// =============================================================================
// New and configuration
// =============================================================================

func (s *LoopSuite) TestNew_RejectsAMissingModelOrExecutor() {
	llm := steerlooptest.NewScriptedModel(says("x"))

	_, err := New(s.cfg, llm, nil, nil, nil)
	s.ErrorContains(err, "tool executor")

	_, err = New(s.cfg, nil, nil, s.exec, nil)
	s.ErrorContains(err, "LLM")
}

func (s *LoopSuite) TestNew_ReportsAToolBindingFailure() {
	_, err := New(s.cfg, failingBinder{err: errors.New("no tools here")}, s.sampleTools(), s.exec, nil)
	s.ErrorContains(err, "no tools here")

	_, err = New(s.cfg, failingBinder{}, s.sampleTools(), s.exec, nil)
	s.ErrorContains(err, "returned no model")
}

func (s *LoopSuite) TestNew_BindsTheToolsToTheModel() {
	llm := steerlooptest.NewScriptedModel(says("x"))

	loop, err := New(s.cfg, llm, s.sampleTools(), s.exec, nil) // a nil logger is allowed
	s.Require().NoError(err)
	s.NotNil(loop)
	s.Require().Len(llm.BoundTools(), 1)
	s.Len(llm.BoundTools()[0], 2)
}

func (s *LoopSuite) TestConfig_DefaultsRunAnAssistantOutOfTheBox() {
	s.cfg = DefaultConfig()
	s.Equal(12, s.cfg.MaxIterations)
	s.Equal(StopOnNoToolCalls, s.cfg.StopCondition)
	s.True(s.cfg.StreamEvents)
	s.Equal(time.Minute, s.cfg.StepTimeout)

	events, _ := s.runScript(says("hi"))
	s.Equal("hi", s.done(events).FinalContent)
}

func (s *LoopSuite) TestConfig_StopConditionsHaveStableLabels() {
	s.Equal("no_tool_calls", StopConditionLabel(StopOnNoToolCalls))
	s.Equal("finish_tool", StopConditionLabel(StopOnFinishTool))
	s.Equal("explicit", StopConditionLabel(StopOnExplicit))
	s.Equal("", StopConditionLabel(StopCondition(99)))
}

func (s *LoopSuite) TestConfig_CallOptionsReplaceTheDefaultOptions() {
	s.cfg.Temperature = 0.3
	_, llm := s.runScript(says("hi"))
	s.Require().NotNil(llm.LastOptions().Temperature)
	s.InDelta(0.3, *llm.LastOptions().Temperature, 0.001, "the default is model.WithTemperature")

	var temperatureSeen float32
	s.cfg.CallOptions = func(temperature float32) []model.Option {
		temperatureSeen = temperature
		return []model.Option{model.WithMaxTokens(77)}
	}
	_, llm = s.runScript(says("hi"))
	s.InDelta(0.3, temperatureSeen, 0.001)
	s.Require().NotNil(llm.LastOptions().MaxTokens)
	s.Equal(77, *llm.LastOptions().MaxTokens)
	s.Nil(llm.LastOptions().Temperature, "the hook replaces the default options")
}

func (s *LoopSuite) TestConfig_HistoryCompactorShapesOnlyWhatTheModelSees() {
	s.cfg.HistoryCompactor = func(msgs []*schema.Message) []*schema.Message {
		return msgs[len(msgs)-1:] // keep only the newest message
	}
	llm := steerlooptest.NewScriptedModel(asksFor(profileCall()), says("answer"))
	loop := s.newLoop(llm)
	s.run(loop)

	for _, call := range llm.Calls() {
		s.Len(call, 1, "the model sees the compacted history")
	}
	s.Equal(schema.Tool, llm.LastMessages()[0].Role)
	s.Len(loop.GetRunningMessages(), 5, "the stored history is not compacted")
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
	s.Same(first, loop.startingMessages[0])
	s.Len(loop.startingMessages, 2)

	running := loop.GetRunningMessages()
	s.Require().Len(running, 3, "the working copy grows")
	s.Same(first, running[0])
	s.Equal(schema.Assistant, running[2].Role)
	s.Equal("Hello there!", running[2].Content)
}

func (s *LoopSuite) TestStart_ASecondCallFailsWithoutDisturbingTheRun() {
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("hello")))
	first := loop.Start(context.Background(), seedConversation())
	second := loop.Start(context.Background(), seedConversation())
	s.NotEqual(first, second, "the second call gets its own channel")

	failure := payload[ErrorData](s, s.collect(second), EventError)
	s.Contains(failure.Error, "already started")
	s.True(failure.Fatal)

	s.Equal(DoneReasonFinished, s.done(s.collect(first)).Reason)
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
	s.Equal(StateDone, loop.GetState())
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
	s.Equal(DoneReasonFinished, done.Reason)
	s.Equal("final", done.FinalContent)
	s.Equal(1, done.Iterations)
}

func (s *LoopSuite) TestState_AccessorsAreSafeForConcurrentUse() {
	s.cfg.StopCondition = StopOnExplicit
	s.cfg.MaxIterations = 5
	loop := s.newLoop(steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "response", Delay: 5 * time.Millisecond}))
	events := loop.Start(context.Background(), seedConversation())

	var readers sync.WaitGroup
	for range 20 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			_ = loop.GetState()
			_ = loop.GetCurrentIteration()
			_ = loop.GetRunningMessages()
		}()
	}
	readers.Wait()

	s.Equal(DoneReasonMaxIterations, s.done(s.collect(events)).Reason)
}

// =============================================================================
// Running: replies, tools and stop conditions
// =============================================================================

func (s *LoopSuite) TestRun_ATextReplyEndsTheRun() {
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("Hello there!")))
	events := s.run(loop)

	s.NotNil(firstEvent(events, EventThinking))
	s.Equal("Hello there!", payload[ResponseData](s, events, EventResponse).Content)
	done := s.done(events)
	s.Equal(DoneReasonFinished, done.Reason)
	s.Equal(1, done.Iterations)
	s.Equal("Hello there!", done.FinalContent)
	s.Zero(s.exec.CallCount())
	s.Equal(StateDone, loop.GetState())
}

func (s *LoopSuite) TestRun_FeedsTheToolResultBackToTheModel() {
	s.exec = steerlooptest.NewRecordingExecutor(func(context.Context, string, string) (string, error) {
		return `{"level":"intermediate"}`, nil
	})
	events, llm := s.runScript(asksFor(profileCall()), says("Based on your profile, here is a lesson."))

	s.Equal(profileTool, payload[ToolCallData](s, events, EventToolCall).Name)
	s.Contains(payload[ToolResultData](s, events, EventToolResult).Result, "intermediate")
	s.Equal([]string{profileTool}, s.exec.CalledTools())

	s.Equal(2, llm.CallCount())
	result := lastMessage(llm)
	s.Equal(schema.Tool, result.Role, "the second call ends with the tool result")
	s.Equal("call_1", result.ToolCallID)
	s.Equal(profileTool, result.ToolName)
	s.Contains(result.Content, "intermediate")

	done := s.done(events)
	s.Equal(DoneReasonFinished, done.Reason)
	s.Equal(2, done.Iterations)
}

func (s *LoopSuite) TestRun_ExecutesEveryToolCallOfATurn() {
	events, _ := s.runScript(
		asksFor(profileCall(), steerlooptest.ToolCall("call_2", searchTool, `{"query":"go concurrency"}`)),
		says("Here is your lesson on Go concurrency."),
	)

	s.Len(eventsOfType(events, EventToolCall), 2)
	s.Len(eventsOfType(events, EventToolResult), 2)
	s.Equal([]string{profileTool, searchTool}, s.exec.CalledTools())
	s.Equal(2, s.done(events).Iterations)
}

func (s *LoopSuite) TestRun_AToolErrorIsReturnedToTheModel() {
	s.exec = steerlooptest.NewRecordingExecutor(func(context.Context, string, string) (string, error) {
		return "", errors.New("database connection failed")
	})
	events, llm := s.runScript(asksFor(profileCall()), says("I couldn't read your profile."))

	s.Equal("database connection failed", payload[ToolResultData](s, events, EventToolResult).Error)
	s.Contains(lastMessage(llm).Content, "database connection failed", "the model sees the error as the tool's result")
	s.Equal(DoneReasonFinished, s.done(events).Reason, "a tool error is not fatal")
}

func (s *LoopSuite) TestRun_AToolPanicBecomesAToolError() {
	s.exec = steerlooptest.NewRecordingExecutor(func(context.Context, string, string) (string, error) {
		panic("boom")
	})
	events, _ := s.runScript(asksFor(profileCall()), says("recovered"))

	result := payload[ToolResultData](s, events, EventToolResult)
	s.Contains(result.Error, "panicked")
	s.Contains(result.Result, "boom")
	s.Equal(DoneReasonFinished, s.done(events).Reason)

	failures := eventsOfType(events, EventError)
	s.Require().Len(failures, 1)
	s.False(failures[0].Data.(ErrorData).Fatal)
}

func (s *LoopSuite) TestRun_TheFinishToolEndsTheRunWithItsArguments() {
	s.cfg.StopCondition = StopOnFinishTool
	finishArgs := `{"title":"Lesson 1","content":"Learn about goroutines..."}`
	events, _ := s.runScript(asksFor(profileCall()), asksFor(finishCall(finishArgs)))

	done := s.done(events)
	s.Equal(DoneReasonFinished, done.Reason)
	s.Equal(finishArgs, done.FinalContent)
	s.Equal(2, done.Iterations)
	s.Equal([]string{profileTool}, s.exec.CalledTools(), "finish never reaches the executor")
}

func (s *LoopSuite) TestRun_ATextReplyInFinishToolModeIsNudged() {
	s.cfg.StopCondition = StopOnFinishTool
	finish := asksFor(finishCall(`{"ok":true}`))

	events, llm := s.runScript(says("thinking aloud"), finish)
	done := s.done(events)
	s.Equal(DoneReasonFinished, done.Reason)
	s.Equal(`{"ok":true}`, done.FinalContent)
	s.Equal(2, done.Iterations)
	s.Equal(defaultAutoNudge, lastMessage(llm).Content, "the built-in nudge follows the text reply")

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
	s.Equal(DoneReasonMaxIterations, done.Reason)
	s.Equal(3, done.Iterations)
	s.Equal(3, s.exec.CallCount())
}

func (s *LoopSuite) TestRun_EndsWhenTheContextIsCancelled() {
	s.cfg.StopCondition = StopOnExplicit
	llm, release := heldModel("response")
	loop := s.newLoop(llm)
	ctx, cancel := context.WithCancel(context.Background())
	events := loop.Start(ctx, seedConversation())
	s.waitForModelCall(llm)

	cancel()
	release()

	s.Equal(DoneReasonTimeout, s.done(s.collect(events)).Reason)
	s.Equal(1, llm.CallCount(), "no model call starts after the cancellation")
}

// =============================================================================
// Timeouts
// =============================================================================

func (s *LoopSuite) TestTimeout_TotalTimeoutEndsTheRunAtTheNextBoundary() {
	s.cfg.StopCondition = StopOnExplicit
	s.cfg.TotalTimeout = 50 * time.Millisecond
	llm, release := heldModel("response")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())
	s.waitForModelCall(llm)

	time.Sleep(100 * time.Millisecond) // the deadline passes while the call is held
	release()

	s.Equal(DoneReasonTimeout, s.done(s.collect(events)).Reason)
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
			s.cfg.StepTimeout = 5 * time.Second
			s.cfg.DisableStepTimeoutDecoupling = tc.disable
			start := time.Now()

			events, _ := s.runScript(steerlooptest.Step{Content: "slow", Delay: 3 * time.Second})

			done := s.done(events)
			s.Equal(DoneReasonError, done.Reason)
			s.Contains(done.Error, tc.want)
			s.Less(time.Since(start), 2*time.Second)
		})
	}
}

func (s *LoopSuite) TestTimeout_StepTimeoutFailsTheRunOnceRetriesAreSpent() {
	s.cfg.StepTimeout = 50 * time.Millisecond
	s.cfg.TotalTimeout = 2 * time.Second
	events, llm := s.runScript(steerlooptest.Step{Content: "slow", Delay: 500 * time.Millisecond})

	failure := payload[ErrorData](s, events, EventError)
	s.True(failure.Fatal)
	s.Contains(failure.Error, "deadline exceeded")
	s.Equal(DoneReasonError, s.done(events).Reason)
	s.Equal(2, llm.CallCount(), "the attempt and its one retry both time out")
}

func (s *LoopSuite) TestTimeout_ZeroStepTimeoutMeansNoBound() {
	s.cfg.StepTimeout = 0
	events, _ := s.runScript(steerlooptest.Step{Content: "ok", Delay: 50 * time.Millisecond})
	s.Equal("ok", s.done(events).FinalContent)
}

// =============================================================================
// Controls: stop, inject and modify config
// =============================================================================

func (s *LoopSuite) TestStop_EndsTheRunAtTheNextIterationBoundary() {
	s.cfg.StopCondition = StopOnExplicit
	llm, release := heldModel("tick")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())
	s.waitForModelCall(llm)

	loop.Stop()
	release()

	s.Equal(DoneReasonStopped, s.done(s.collect(events)).Reason)
	s.Equal(StateStopped, loop.GetState())
	s.Equal(1, llm.CallCount(), "the call in flight completes; no new call starts")
}

func (s *LoopSuite) TestStop_CannotBeLostWhenTheControlQueueIsFull() {
	s.cfg.StopCondition = StopOnExplicit
	llm, release := heldModel("working")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())
	s.waitForModelCall(llm)

	temperature := 0.1
	for range controlChBuffer {
		s.Require().True(loop.SendControlSignal(NewModifyConfigControl(ModifyConfigData{Temperature: &temperature})))
	}
	s.False(loop.SendControlSignal(NewStopControl()), "a stop signal is lost when the queue is full")
	loop.Stop()
	release()

	s.Equal(DoneReasonStopped, s.done(s.collect(events)).Reason)
	s.Equal(StateStopped, loop.GetState())
	s.Equal(1, llm.CallCount(), "no model call starts after Stop")
}

func (s *LoopSuite) TestStop_IsIdempotent() {
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("x")))
	loop.Stop()
	loop.Stop()
	s.NotNil(loop.Events())
}

func (s *LoopSuite) TestControl_AFullQueueRejectsNewSignals() {
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("x")))
	for range controlChBuffer {
		s.Require().True(loop.Inject("m"))
	}
	s.False(loop.Inject("one too many"))
	s.False(loop.SendControlSignal(NewStopControl()))
}

func (s *LoopSuite) TestControl_IgnoresEmptyInjectsAndUnknownSignals() {
	s.cfg.StopCondition = StopOnExplicit
	s.cfg.MaxIterations = 2
	llm, release := heldModel("tick")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())

	s.Require().True(loop.Inject(""))
	s.Require().True(loop.SendControlSignal(Control{Type: "unknown"}))
	s.Require().True(loop.SendControlSignal(Control{Type: ControlModifyConfig, Data: "wrong payload"}))
	release()

	all := s.collect(events)
	s.Empty(eventsOfType(all, EventProgress))
	s.Len(loop.GetRunningMessages(), 4, "two seed messages and two replies; nothing was injected")
}

func (s *LoopSuite) TestInject_AddsAUserMessageBeforeTheNextModelCall() {
	s.cfg.StopCondition = StopOnExplicit
	s.cfg.MaxIterations = 2
	llm, release := heldModel("tick")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())
	s.waitForModelCall(llm)

	s.Require().True(loop.Inject("use Python examples"))
	release()

	all := s.collect(events)
	s.Equal(DoneReasonMaxIterations, s.done(all).Reason)
	s.Equal("inject", payload[ProgressData](s, all, EventProgress).Step)

	calls := llm.Calls()
	s.Require().Len(calls, 2)
	s.Len(calls[0], 2, "the call in flight is unaffected")
	injected := calls[1][len(calls[1])-1]
	s.Equal(schema.User, injected.Role)
	s.Equal("use Python examples", injected.Content)
}

func (s *LoopSuite) TestModifyConfig_RaisesTheIterationCapAndTogglesReflection() {
	s.cfg.StopCondition = StopOnExplicit
	s.cfg.MaxIterations = 2
	llm, release := heldModel("tick")
	loop := s.newLoop(llm)
	events := loop.Start(context.Background(), seedConversation())

	five, on := 5, true
	s.Require().True(loop.SendControlSignal(NewModifyConfigControl(ModifyConfigData{MaxIterations: &five, EnableReflection: &on})))
	release()

	all := s.collect(events)
	done := s.done(all)
	s.Equal(DoneReasonMaxIterations, done.Reason)
	s.Equal(5, done.Iterations)
	s.True(loop.reflectionEnabled())

	progress := eventsOfType(all, EventProgress)
	s.Require().Len(progress, 1)
	s.Equal("config_modify", progress[0].Data.(ProgressData).Step)
}

func (s *LoopSuite) TestModifyConfig_DuringAPauseIsRaceFree() {
	// Configuration changes arrive while the pause makes model calls from
	// another goroutine. Run with -race.
	blocked := make(chan struct{})
	loop, _ := s.pausingLoop(func(ctx context.Context, in PauseIn) (string, bool) {
		var sideCalls sync.WaitGroup
		sideCalls.Add(1)
		go func() {
			defer sideCalls.Done()
			for range 50 {
				_, _ = in.Call(ctx, nil, in.Messages)
				in.Emit(EventProgress, ProgressData{Message: "side"})
			}
		}()
		close(blocked)
		reply, ok := in.Block(ctx)
		sideCalls.Wait()
		return reply, ok
	})
	events := loop.Start(context.Background(), seedConversation())
	go func() {
		for range events { // keep the channel drained
		}
	}()

	<-blocked
	for i := range 50 {
		temperature := float64(i) / 100
		for !loop.SendControlSignal(NewModifyConfigControl(ModifyConfigData{Temperature: &temperature})) {
			time.Sleep(time.Millisecond)
		}
		_ = loop.GetState()
	}
	for !loop.Inject("the answer") {
		time.Sleep(time.Millisecond)
	}
	loop.Wait()

	done, ok := loop.Result()
	s.Require().True(ok)
	s.Equal(DoneReasonFinished, done.Reason)
}

// =============================================================================
// Pauses
// =============================================================================

func (s *LoopSuite) TestPause_TheReplyBecomesTheToolResult() {
	var seen PauseIn
	loop, llm := s.pausingLoop(func(_ context.Context, in PauseIn) (string, bool) {
		seen = in
		return "blue", true
	})
	events := s.run(loop)

	s.Equal(DoneReasonFinished, s.done(events).Reason)
	s.Zero(s.exec.CallCount(), "a pause tool never reaches the executor")
	s.Equal("call_1", seen.CallID)
	s.Equal(askUserTool, seen.Name)
	s.JSONEq(`{"question":"colour?"}`, seen.Arguments)
	s.Len(seen.Messages, 3, "the seed messages plus the assistant tool call")
	s.Equal("blue", payload[ToolResultData](s, events, EventToolResult).Result)

	result := lastMessage(llm)
	s.Equal(schema.Tool, result.Role)
	s.Equal("blue", result.Content)
	s.Equal("call_1", result.ToolCallID)
}

func (s *LoopSuite) TestPause_AnEmptyReplyIsFilledIn() {
	loop, llm := s.pausingLoop(func(context.Context, PauseIn) (string, bool) { return "", true })
	s.run(loop)
	s.Equal("(no response)", lastMessage(llm).Content)
}

func (s *LoopSuite) TestPause_BlockReturnsTheNextInject() {
	blocked := make(chan struct{})
	loop, llm := s.pausingLoop(func(ctx context.Context, in PauseIn) (string, bool) {
		in.Emit(EventAwaitingInput, AwaitingInputData{Question: "colour?"})
		close(blocked)
		return in.Block(ctx)
	})
	events := loop.Start(context.Background(), seedConversation())
	<-blocked
	s.Require().True(loop.Inject("green"))

	all := s.collect(events)
	s.Equal(DoneReasonFinished, s.done(all).Reason)
	s.Equal("colour?", payload[AwaitingInputData](s, all, EventAwaitingInput).Question)
	s.Equal("green", lastMessage(llm).Content)
}

func (s *LoopSuite) TestPause_StopWhileBlockedEndsTheRunAtOnce() {
	for _, tc := range []struct {
		name string
		stop func(*Loop)
	}{
		{"Stop", func(loop *Loop) { loop.Stop() }},
		{"stop signal", func(loop *Loop) { s.Require().True(loop.SendControlSignal(NewStopControl())) }},
	} {
		s.Run(tc.name, func() {
			loop, llm, blocked := s.blockedPausingLoop()
			events := loop.Start(context.Background(), seedConversation())
			<-blocked

			tc.stop(loop)

			s.Equal(DoneReasonStopped, s.done(s.collect(events)).Reason)
			s.Equal(StateStopped, loop.GetState())
			s.Equal(1, llm.CallCount())
		})
	}
}

func (s *LoopSuite) TestPause_ContextCancellationWhileBlockedStopsTheRun() {
	loop, _, blocked := s.blockedPausingLoop()
	ctx, cancel := context.WithCancel(context.Background())
	events := loop.Start(ctx, seedConversation())
	<-blocked

	cancel()

	s.Equal(DoneReasonStopped, s.done(s.collect(events)).Reason)
	s.Equal(StateStopped, loop.GetState())
}

func (s *LoopSuite) TestPause_ConfigChangesApplyWhileBlocked() {
	loop, llm, blocked := s.blockedPausingLoop()
	events := loop.Start(context.Background(), seedConversation())
	<-blocked

	temperature := 0.9
	s.Require().True(loop.SendControlSignal(NewModifyConfigControl(ModifyConfigData{Temperature: &temperature})))
	s.Require().True(loop.Inject("ok"))

	s.collect(events)
	s.Require().NotNil(llm.LastOptions().Temperature)
	s.InDelta(0.9, *llm.LastOptions().Temperature, 0.001)
}

func (s *LoopSuite) TestPause_EndingWithoutAReplyHandsOverToAHuman() {
	type payloadForUI struct{ ID string }
	loop, llm := s.pausingLoop(func(_ context.Context, in PauseIn) (string, bool) {
		in.Emit(EventAwaitingInput, AwaitingInputData{
			Question: "Which colour?",
			Options:  []string{"red", "blue"},
			Payload:  payloadForUI{ID: "g-1"},
		})
		return "", false
	})
	events := s.run(loop)

	done := s.done(events)
	s.Equal(DoneReasonAwaitingHuman, done.Reason)
	s.Equal("Which colour?", done.FinalContent)
	s.Require().NotNil(done.Awaiting)
	s.Equal([]string{"red", "blue"}, done.Awaiting.Options)
	s.Equal(payloadForUI{ID: "g-1"}, done.Awaiting.Payload)
	s.Equal(StateDone, loop.GetState())
	s.Equal(1, llm.CallCount())
	s.Nil(firstEvent(events, EventToolResult), "no tool result is recorded for an unanswered pause")
}

func (s *LoopSuite) TestPause_AwaitingInputAcceptsAPointerPayload() {
	loop, _ := s.pausingLoop(func(_ context.Context, in PauseIn) (string, bool) {
		in.Emit(EventAwaitingInput, &AwaitingInputData{Question: "Q?"})
		return "", false
	})
	s.Equal(DoneReasonAwaitingHuman, s.done(s.run(loop)).Reason)
}

func (s *LoopSuite) TestPause_EndingWithoutAwaitingInputStopsTheRun() {
	for _, reply := range []string{"", "bye"} {
		s.Run(fmt.Sprintf("reply %q", reply), func() {
			loop, _ := s.pausingLoop(func(context.Context, PauseIn) (string, bool) { return reply, false })
			s.Equal(DoneReasonStopped, s.done(s.run(loop)).Reason)
		})
	}
}

func (s *LoopSuite) TestPause_CanRunToolsAndCallTheModel() {
	lookup, err := NewToolInfo("lookup", "Look something up", nil)
	s.Require().NoError(err)

	var toolOut, modelOut string
	loop, llm := s.pausingLoop(func(ctx context.Context, in PauseIn) (string, bool) {
		toolOut, _ = in.Execute(ctx, "lookup", `{"q":1}`)
		if reply, err := in.Call(ctx, []*schema.ToolInfo{lookup}, in.Messages); err == nil && reply != nil {
			modelOut = reply.Content
		}
		return "done", true
	})
	s.run(loop)

	s.Equal("result from lookup", toolOut)
	s.Equal([]string{"lookup"}, s.exec.CalledTools())
	s.Equal("thanks", modelOut, "Call plays the next scripted reply")
	s.Require().Len(llm.BoundTools(), 1)
	s.Equal("lookup", llm.BoundTools()[0][0].Name)
}

func (s *LoopSuite) TestPause_APanicBecomesAToolError() {
	loop, llm := s.pausingLoop(func(context.Context, PauseIn) (string, bool) { panic("pause boom") })
	events := s.run(loop)

	s.Equal(DoneReasonFinished, s.done(events).Reason)
	s.Contains(lastMessage(llm).Content, "pause boom")
	s.False(payload[ErrorData](s, events, EventError).Fatal)
}

func (s *LoopSuite) TestPause_OtherToolsStillReachTheExecutor() {
	s.cfg.Pauses = map[string]PauseFunc{
		askUserTool: func(context.Context, PauseIn) (string, bool) { return "yes", true },
	}
	events, _ := s.runScript(
		asksFor(
			profileCall(),
			steerlooptest.ToolCall("call_2", askUserTool, `{}`),
			steerlooptest.ToolCall("call_3", searchTool, `{"query":"x"}`),
		),
		says("ok"),
	)

	s.Equal([]string{profileTool, searchTool}, s.exec.CalledTools())
	s.Len(eventsOfType(events, EventToolResult), 3)
}

// =============================================================================
// Events
// =============================================================================

func (s *LoopSuite) TestEvents_TheSinkSeesEveryEventInOrder() {
	var sunk []EventType
	s.cfg.EventSink = func(_ context.Context, evt Event) { sunk = append(sunk, evt.Type) }
	events, _ := s.runScript(asksFor(profileCall()), says("done"))

	var fromChannel []EventType
	for _, evt := range events {
		fromChannel = append(fromChannel, evt.Type)
	}
	s.Equal(fromChannel, sunk)
	s.Equal([]EventType{
		EventThinking, EventLLMTurn, EventToolCall, EventToolResult,
		EventThinking, EventResponse, EventDone,
	}, sunk)
}

func (s *LoopSuite) TestEvents_StreamEventsOffEmitsOnlyTheOutcome() {
	var sunk []EventType
	s.cfg.StreamEvents = false
	s.cfg.EventSink = func(_ context.Context, evt Event) { sunk = append(sunk, evt.Type) }
	events, _ := s.runScript(asksFor(profileCall()), says("Here is the lesson."))

	s.Require().Len(events, 1)
	s.Equal(EventDone, events[0].Type)
	s.Equal([]EventType{EventDone}, sunk)
}

func (s *LoopSuite) TestEvents_CarryATimestampAndTheirIteration() {
	before := time.Now()
	events, _ := s.runScript(asksFor(profileCall()), says("Response after tools."))
	after := time.Now()

	for _, evt := range events {
		s.False(evt.Timestamp.Before(before), "%s is stamped before the run", evt.Type)
		s.False(evt.Timestamp.After(after), "%s is stamped after the run", evt.Type)
	}
	thinking := eventsOfType(events, EventThinking)
	s.Require().Len(thinking, 2)
	s.Equal(1, thinking[0].Iteration)
	s.Equal(2, thinking[1].Iteration)
	s.Equal(2, firstEvent(events, EventDone).Iteration)
}

func (s *LoopSuite) TestEvents_AnUnreadLoopDropsAndCountsThem() {
	s.cfg.StopCondition = StopOnExplicit
	s.cfg.MaxIterations = 20 // 40 intermediate events
	s.cfg.EventBuffer = 8
	s.cfg.DoneDeliveryGrace = 30 * time.Millisecond
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("tick")))
	loop.Start(context.Background(), seedConversation())
	loop.Wait()

	done, ok := loop.Result()
	s.Require().True(ok)
	s.Equal(DoneReasonMaxIterations, done.Reason)
	s.Equal(32, done.DroppedEvents)
	s.Equal(32, s.logs.count("agent loop event channel full, dropping event"))
	s.Equal(1, s.logs.count("agent loop done event not read within grace period; use Result"))
}

func (s *LoopSuite) TestEvents_DoneReachesAReaderThatStartsLate() {
	s.cfg.StopCondition = StopOnExplicit
	s.cfg.MaxIterations = 80 // 160 intermediate events against a 64-slot channel
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("still working")))
	events := loop.Start(context.Background(), seedConversation())
	time.Sleep(300 * time.Millisecond) // the loop has filled the channel by now

	s.Equal(DoneReasonMaxIterations, s.done(s.collect(events)).Reason)
}

// =============================================================================
// Retries
// =============================================================================

func (s *LoopSuite) TestRetry_RecoversFromATransientError() {
	events, llm := s.runScript(fails(io.ErrUnexpectedEOF), says("recovered"))

	done := s.done(events)
	s.Equal(DoneReasonFinished, done.Reason)
	s.Equal("recovered", done.FinalContent)
	s.Equal(2, llm.CallCount())
	s.Equal(1, done.Iterations, "a retry is not a new iteration")
}

func (s *LoopSuite) TestRetry_AllowsOneRetryByDefault() {
	events, llm := s.runScript(fails(errors.New("read: connection reset by peer")))

	done := s.done(events)
	s.Equal(DoneReasonError, done.Reason)
	s.Contains(done.Error, "connection reset")
	s.Equal(2, llm.CallCount(), "the attempt and one retry")
}

func (s *LoopSuite) TestRetry_ANonTransientErrorIsFatalAtOnce() {
	llm := steerlooptest.NewScriptedModel(fails(errors.New("invalid api key")))
	loop := s.newLoop(llm)
	events := s.run(loop)

	failure := payload[ErrorData](s, events, EventError)
	s.True(failure.Fatal)
	s.Contains(failure.Error, "invalid api key")
	s.Equal(DoneReasonError, s.done(events).Reason)
	s.Equal(StateError, loop.GetState())
	s.Equal(1, llm.CallCount())
}

func (s *LoopSuite) TestRetry_CanBeDisabled() {
	s.cfg.RetryMaxAttempts = -1
	events, llm := s.runScript(fails(io.EOF), says("never"))

	s.Equal(DoneReasonError, s.done(events).Reason)
	s.Equal(1, llm.CallCount())
}

func (s *LoopSuite) TestRetry_HonoursTheConfiguredAttempts() {
	s.cfg.RetryMaxAttempts = 2
	events, llm := s.runScript(fails(io.EOF), fails(io.EOF), says("third time"))

	s.Equal("third time", s.done(events).FinalContent)
	s.Equal(3, llm.CallCount())
}

func (s *LoopSuite) TestRetry_AnEmptyReplyIsRetried() {
	events, llm := s.runScript(steerlooptest.Step{}, says("now with text"))

	s.Equal("now with text", s.done(events).FinalContent)
	s.Equal(2, llm.CallCount())
}

func (s *LoopSuite) TestRetry_APersistentlyEmptyReplyIsFatal() {
	events, llm := s.runScript(steerlooptest.Step{}) // the empty step repeats

	failure := payload[ErrorData](s, events, EventError)
	s.True(failure.Fatal)
	s.Contains(failure.Error, "empty LLM response")
	s.Equal(2, llm.CallCount())
}

func (s *LoopSuite) TestRetry_ACustomClassifierReplacesTheDefault() {
	s.cfg.IsTransient = func(err error) bool { return err != nil && err.Error() == "rate limited" }

	events, llm := s.runScript(fails(errors.New("rate limited")), says("ok"))
	s.Equal("ok", s.done(events).FinalContent)
	s.Equal(2, llm.CallCount())

	events, llm = s.runScript(fails(io.EOF))
	s.Equal(DoneReasonError, s.done(events).Reason)
	s.Equal(1, llm.CallCount(), "EOF is no longer transient")
}

func (s *LoopSuite) TestRetry_AStepTimeoutIsRetried() {
	s.cfg.StepTimeout = 40 * time.Millisecond
	events, llm := s.runScript(
		steerlooptest.Step{Content: "too slow", Delay: 500 * time.Millisecond},
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

	s.Equal(DoneReasonError, s.done(events).Reason)
	s.Less(time.Since(start), 2*time.Second, "the backoff must not outlive the run")
}

func (s *LoopSuite) TestRetry_TheDefaultClassifierKnowsTransientFailures() {
	for _, err := range []error{
		context.DeadlineExceeded, io.EOF, io.ErrUnexpectedEOF, errEmptyResponse,
		fmt.Errorf("wrapped: %w", io.EOF),
		errors.New("stream idle: no chunk received for 30s"),
		errors.New("provider returned Empty Response"),
		errors.New("no response from upstream"),
		errors.New("Connection Reset by peer"),
	} {
		s.True(IsTransientLLMError(err), err.Error())
	}
	for _, err := range []error{nil, context.Canceled, errors.New("401 unauthorized")} {
		s.False(IsTransientLLMError(err))
	}
}

// =============================================================================
// Streaming
// =============================================================================

func (s *LoopSuite) TestStream_ChunksAreJoinedIntoOneReply() {
	s.cfg.DisableStreaming = false
	events, _ := s.runScript(steerlooptest.Step{
		Chunks: []steerlooptest.Chunk{{Content: "Hel"}, {Content: "lo "}, {Content: "there"}},
	})
	s.Equal("Hello there", s.done(events).FinalContent)
}

func (s *LoopSuite) TestStream_ToolCallsArriveOnTheLastChunk() {
	s.cfg.DisableStreaming = false
	events, _ := s.runScript(
		steerlooptest.Step{
			Chunks:    []steerlooptest.Chunk{{Content: ""}, {Content: ""}},
			ToolCalls: []schema.ToolCall{profileCall()},
		},
		says("used the tool"),
	)

	s.Equal("used the tool", s.done(events).FinalContent)
	s.Equal([]string{profileTool}, s.exec.CalledTools())
}

func (s *LoopSuite) TestStream_TheIdleWatchdogAbortsAStalledStream() {
	s.cfg.DisableStreaming = false
	s.cfg.IdleChunkTimeout = 40 * time.Millisecond
	s.cfg.RetryMaxAttempts = -1
	events, _ := s.runScript(steerlooptest.Step{
		Chunks: []steerlooptest.Chunk{{Content: "start"}, {Content: "never", Delay: 2 * time.Second}},
	})

	done := s.done(events)
	s.Equal(DoneReasonError, done.Reason)
	s.Contains(done.Error, "stream idle")
}

func (s *LoopSuite) TestStream_TheIdleWatchdogWaitsForTheFirstChunk() {
	s.cfg.DisableStreaming = false
	s.cfg.IdleChunkTimeout = 40 * time.Millisecond
	events, llm := s.runScript(steerlooptest.Step{
		Chunks: []steerlooptest.Chunk{{Content: "late but fine", Delay: 200 * time.Millisecond}},
	})

	s.Equal("late but fine", s.done(events).FinalContent)
	s.Equal(1, llm.CallCount())
}

func (s *LoopSuite) TestStream_AStalledStreamIsRetried() {
	s.cfg.DisableStreaming = false
	s.cfg.IdleChunkTimeout = 40 * time.Millisecond
	events, llm := s.runScript(
		steerlooptest.Step{Chunks: []steerlooptest.Chunk{{Content: "a"}, {Content: "b", Delay: 2 * time.Second}}},
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
	llm := &countingModel{inner: steerlooptest.NewScriptedModel(says("hi"))}
	for _, disable := range []bool{true, false} {
		s.cfg.DisableStreaming = disable
		s.run(s.newLoop(llm))
	}
	s.Equal(1, llm.generate)
	s.Equal(1, llm.stream)
}

// =============================================================================
// Reflection
// =============================================================================

func (s *LoopSuite) TestReflection_AddsASelfReviewAfterTheReply() {
	s.cfg.EnableReflection = true
	events, llm := s.runScript(says("Here is the lesson."), says("The reply looks complete."))

	s.Equal("The reply looks complete.", payload[ReflectionData](s, events, EventReflection).Content)
	s.Equal(2, llm.CallCount(), "the reply and its review")
	s.Equal("Here is the lesson.", s.done(events).FinalContent, "the review does not replace the reply")
}

func (s *LoopSuite) TestReflection_ThePromptIsConfigurable() {
	s.cfg.EnableReflection = true
	_, llm := s.runScript(says("the answer"), says("looks fine"))
	s.Equal(defaultReflectionPrompt+"\n\nYour response was:\nthe answer", lastMessage(llm).Content)

	s.cfg.ReflectionPrompt = "Check this for a beginner."
	_, llm = s.runScript(says("the answer"), says("looks fine"))
	s.Equal("Check this for a beginner.\n\nYour response was:\nthe answer", lastMessage(llm).Content)
}

func (s *LoopSuite) TestReflection_AFailureIsNotFatal() {
	s.cfg.EnableReflection = true
	s.cfg.RetryMaxAttempts = -1
	events, _ := s.runScript(says("the answer"), fails(errors.New("quota")))

	s.Equal(DoneReasonFinished, s.done(events).Reason)
	failure := payload[ErrorData](s, events, EventError)
	s.False(failure.Fatal)
	s.Contains(failure.Error, "reflection failed")
}

// =============================================================================
// Audit and usage
// =============================================================================

// auditRecord is the shape of the JSON written for a model request.
type auditRecord struct {
	Messages []struct {
		Role       string          `json:"role"`
		Content    string          `json:"content"`
		ToolCalls  json.RawMessage `json:"tool_calls"`
		ToolCallID string          `json:"tool_call_id"`
		ToolName   string          `json:"tool_name"`
	} `json:"messages"`
	Truncated bool `json:"truncated"`
}

func (s *LoopSuite) TestAudit_SerializesTheRequestMessages() {
	raw, count, chars := SerializeEinoMessagesForAudit([]*schema.Message{
		{Role: schema.System, Content: "sys"},
		nil,
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{steerlooptest.ToolCall("c1", "lookup", `{"q":1}`)}},
		{Role: schema.Tool, Content: "found", ToolCallID: "c1", ToolName: "lookup"},
	})

	var record auditRecord
	s.Require().NoError(json.Unmarshal(raw, &record))
	s.False(record.Truncated)
	s.Require().Len(record.Messages, 3, "nil messages are skipped")
	s.EqualValues(3, count)
	s.Equal("system", record.Messages[0].Role)
	s.Contains(string(record.Messages[1].ToolCalls), `"lookup"`)
	s.Equal("c1", record.Messages[2].ToolCallID)
	s.Equal("lookup", record.Messages[2].ToolName)
	s.EqualValues(len("sys")+len(record.Messages[1].ToolCalls)+len("found"), chars)
}

func (s *LoopSuite) TestAudit_AnEmptyRequestIsAnEmptyList() {
	raw, count, chars := SerializeEinoMessagesForAudit(nil)
	s.JSONEq(`{"messages":[]}`, string(raw))
	s.Zero(count)
	s.Zero(chars)
}

func (s *LoopSuite) TestAudit_DropsTheOldestMessagesFirstWhenOverTheCap() {
	big := strings.Repeat("x", 100*1024)
	raw, count, _ := SerializeEinoMessagesForAudit([]*schema.Message{
		{Role: schema.User, Content: "first " + big},
		{Role: schema.User, Content: "second " + big},
		{Role: schema.User, Content: "third " + big},
	})

	s.LessOrEqual(len(raw), maxAuditRequestJSONBytes)
	var record auditRecord
	s.Require().NoError(json.Unmarshal(raw, &record))
	s.True(record.Truncated)
	s.Require().Len(record.Messages, 2)
	s.EqualValues(2, count)
	s.True(strings.HasPrefix(record.Messages[0].Content, "second "), "the oldest message is dropped first")
}

func (s *LoopSuite) TestAudit_DropsASingleOversizedMessage() {
	raw, _, _ := SerializeEinoMessagesForAudit([]*schema.Message{
		{Role: schema.User, Content: strings.Repeat("x", 300*1024)},
	})
	s.JSONEq(`{"messages":[],"truncated":true}`, string(raw))
}

func (s *LoopSuite) TestAudit_SerializesTheAssistantToolCalls() {
	s.Equal("[]", string(SerializeAssistantToolCalls(nil)))
	s.Equal("[]", string(SerializeAssistantToolCalls(&schema.Message{})))

	raw := SerializeAssistantToolCalls(&schema.Message{ToolCalls: []schema.ToolCall{steerlooptest.ToolCall("c1", "lookup", `{}`)}})
	s.Contains(string(raw), `"id":"c1"`)
}

func (s *LoopSuite) TestAudit_ClassifiesTheReply() {
	s.Equal("error", responseKindForMessage(nil))
	s.Equal("text", responseKindForMessage(&schema.Message{Content: "x"}))
	s.Equal("tool_calls", responseKindForMessage(&schema.Message{ToolCalls: []schema.ToolCall{profileCall()}}))
}

func (s *LoopSuite) TestAudit_TheLLMTurnEventCarriesUsageIdentityAndCost() {
	steps := []steerlooptest.Step{
		{ToolCalls: []schema.ToolCall{profileCall()}, Usage: &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 20}},
		{Content: "answer", Usage: &schema.TokenUsage{PromptTokens: 150, CompletionTokens: 30}},
	}

	events, _ := s.runScript(steps...)
	turn := payload[LLMTurnData](s, events, EventLLMTurn)
	s.Equal(1, turn.TurnIndex)
	s.Equal(100, turn.InputTokens)
	s.Equal(20, turn.OutputTokens)
	s.Zero(turn.CostAmount)
	s.Empty(turn.Provider)
	s.Equal("tool_calls", turn.ResponseKind)
	s.Equal("no_tool_calls", turn.StopCondition)
	s.Equal(10, turn.MaxIterations)
	s.JSONEq(`[{"id":"call_1","type":"function","function":{"name":"get_profile","arguments":"{}"}}]`, string(turn.AssistantToolCallsJSON))
	var request struct {
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	s.Require().NoError(json.Unmarshal(turn.RequestMessagesJSON, &request))
	s.Require().Len(request.Messages, 2)
	s.Equal("user", request.Messages[1].Role)
	s.Equal(LLMUsageSummary{InputTokens: 250, OutputTokens: 50, TotalTokens: 300}, s.done(events).LLMUsage)

	var pricedProvider, pricedModel string
	s.cfg.ModelIdentity = func() (string, string) { return "acme", "big-1" }
	s.cfg.CostEstimator = func(provider, modelName string, in, out int) float64 {
		pricedProvider, pricedModel = provider, modelName
		return float64(in)*0.01 + float64(out)*0.1
	}
	events, _ = s.runScript(steps...)
	turn = payload[LLMTurnData](s, events, EventLLMTurn)
	s.Equal("acme", turn.Provider)
	s.Equal("big-1", turn.Model)
	s.Equal("acme", pricedProvider)
	s.Equal("big-1", pricedModel)
	s.InDelta(3.0, turn.CostAmount, 0.0001)
}

func (s *LoopSuite) TestUsage_CountsProvidersThatReportOnlyATotal() {
	events, _ := s.runScript(steerlooptest.Step{Content: "hi", Usage: &schema.TokenUsage{TotalTokens: 42}})
	s.Equal(LLMUsageSummary{TotalTokens: 42}, s.done(events).LLMUsage)
}

// =============================================================================
// Registry
// =============================================================================

func (s *LoopSuite) TestRegistry_StopsAndSteersALoopByKey() {
	s.cfg.StopCondition = StopOnExplicit
	llm, release := heldModel("tick")
	loop := s.newLoop(llm)
	reg := NewRegistry[string]()

	s.False(reg.Has("session-1"))
	s.ErrorIs(reg.Stop("session-1"), ErrNoActiveLoop)
	s.ErrorIs(reg.Inject("session-1", "hi"), ErrNoActiveLoop)
	_, err := reg.RunningMessages("session-1")
	s.ErrorIs(err, ErrNoActiveLoop)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := loop.Start(ctx, seedConversation())
	s.waitForModelCall(llm)
	reg.Register("session-1", loop, cancel)
	reg.Register("ignored", nil, nil)
	s.True(reg.Has("session-1"))
	s.False(reg.Has("ignored"))

	active, ok := reg.Get("session-1")
	s.Require().True(ok)
	s.Same(loop, active.Loop)

	s.Require().NoError(reg.Inject("session-1", "steer left"))
	msgs, err := reg.RunningMessages("session-1")
	s.Require().NoError(err)
	s.Len(msgs, 2)
	for range controlChBuffer - 1 {
		s.Require().NoError(reg.Inject("session-1", "more"))
	}
	s.ErrorIs(reg.Inject("session-1", "overflow"), ErrControlQueueFull)

	s.Require().NoError(reg.Stop("session-1"))
	release()
	s.Equal(DoneReasonStopped, s.done(s.collect(events)).Reason)

	reg.Unregister("session-1")
	s.False(reg.Has("session-1"))
}

func (s *LoopSuite) TestRegistry_ToleratesANilReceiverAndStructKeys() {
	var nilReg *Registry[int]
	nilReg.Register(1, nil, nil)
	nilReg.Unregister(1)
	s.False(nilReg.Has(1))
	s.ErrorIs(nilReg.Stop(1), ErrNoActiveLoop)

	type key struct{ Tenant, Run string }
	reg := NewRegistry[key]()
	reg.Register(key{"a", "1"}, s.newLoop(steerlooptest.NewScriptedModel(says("x"))), nil)
	s.True(reg.Has(key{"a", "1"}))
	s.False(reg.Has(key{"a", "2"}))
}

func (s *LoopSuite) TestRegistry_IsSafeForConcurrentUse() {
	reg := NewRegistry[string]()
	loop := s.newLoop(steerlooptest.NewScriptedModel(says("x")))

	var workers sync.WaitGroup
	for i := range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			key := fmt.Sprintf("k%d", i%4)
			for range 200 {
				reg.Register(key, loop, nil)
				reg.Has(key)
				_, _ = reg.RunningMessages(key)
				reg.Unregister(key)
			}
		}()
	}
	workers.Wait()
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

func (s *LoopSuite) TestTranscript_LogsTheSeedOnceThenEachDiff() {
	loop := &Loop{
		cfg:    LoopConfig{TranscriptLogLevel: slog.LevelInfo},
		logger: slog.New(s.logs),
		runningMessages: []*schema.Message{
			{Role: schema.System, Content: "sys"},
			{Role: schema.User, Content: "hi"},
		},
	}
	s.Equal("0 system sys\n1 user hi", formatTranscript(0, loop.runningMessages))
	loop.logged = len(loop.runningMessages)

	ctx := context.Background()
	loop.appendRunningMessages(&schema.Message{Role: schema.Assistant, Content: "looked"})
	loop.logRunningMessages(ctx, 1)
	loop.appendRunningMessages(&schema.Message{Role: schema.Tool, Content: "outline"})
	loop.logRunningMessages(ctx, 2)
	loop.logRunningMessages(ctx, 3) // nothing new to log

	entries := s.logs.all()
	s.Require().Len(entries, 2)
	s.Equal("agent loop diff", entries[0].msg)
	s.Equal("2 assistant looked", entries[0].attrs["diff"])
	s.Equal("3 tool outline", entries[1].attrs["diff"])
	s.Equal(4, loop.logged)
}

// =============================================================================
// Tools
// =============================================================================

func (s *LoopSuite) TestTool_NewToolInfoBuildsADefinitionFromJSONSchema() {
	info, err := NewToolInfo("ping", "Ping", nil)
	s.Require().NoError(err)
	s.Equal("ping", info.Name)
	s.Nil(info.ParamsOneOf, "nil params mean no parameters")

	info, err = NewToolInfo("search", "Search", map[string]any{
		"type":       "object",
		"properties": map[string]any{"q": map[string]any{"type": "string"}},
		"required":   []string{"q"},
	})
	s.Require().NoError(err)
	js, err := info.ToJSONSchema()
	s.Require().NoError(err)
	s.Equal([]string{"q"}, js.Required)

	_, err = NewToolInfo("bad", "", map[string]any{"type": make(chan int)})
	s.ErrorContains(err, "marshal params")
	_, err = NewToolInfo("bad", "", map[string]any{"properties": "not an object"})
	s.ErrorContains(err, "parse params")
}

func (s *LoopSuite) TestTool_ExecutorFuncAdaptsAPlainFunction() {
	var exec ToolExecutor = ToolExecutorFunc(func(_ context.Context, name, args string) (string, error) {
		return name + ":" + args, nil
	})
	out, err := exec.ExecuteTool(context.Background(), "echo", "{}")
	s.Require().NoError(err)
	s.Equal("echo:{}", out)
}

// =============================================================================
// steerlooptest: the scripted model, the recording executor and scripts
// =============================================================================

func userMessage(text string) []*schema.Message {
	return []*schema.Message{{Role: schema.User, Content: text}}
}

func (s *LoopSuite) TestScriptedModel_PlaysTheStepsThenRepeatsTheLast() {
	m := steerlooptest.NewScriptedModel(says("one"), fails(errors.New("boom")), says("three"))
	ctx := context.Background()

	msg, err := m.Generate(ctx, userMessage("a"), model.WithTemperature(0.5))
	s.Require().NoError(err)
	s.Equal("one", msg.Content)
	s.Equal(schema.Assistant, msg.Role)

	_, err = m.Generate(ctx, userMessage("b"))
	s.EqualError(err, "boom")

	for range 2 {
		msg, err = m.Generate(ctx, userMessage("c"))
		s.Require().NoError(err)
		s.Equal("three", msg.Content)
	}
	s.Equal(4, m.CallCount())
	s.Len(m.Calls(), 4)
	s.Equal("c", m.LastMessages()[0].Content)
}

func (s *LoopSuite) TestScriptedModel_WithoutStepsRejectsEveryCall() {
	m := steerlooptest.NewScriptedModel()
	s.Nil(m.LastMessages())
	s.Nil(m.LastOptions())

	_, err := m.Generate(context.Background(), userMessage("a"))
	s.ErrorContains(err, "no steps")
	_, err = m.Stream(context.Background(), userMessage("a"))
	s.ErrorContains(err, "no steps")
}

func (s *LoopSuite) TestScriptedModel_ADelayHonoursTheContext() {
	m := steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "late", Delay: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := m.Generate(ctx, userMessage("a"))
	s.ErrorIs(err, context.DeadlineExceeded)
	_, err = m.Stream(ctx, userMessage("a"))
	s.ErrorIs(err, context.DeadlineExceeded)
}

func (s *LoopSuite) TestScriptedModel_StreamsChunksAndJoinsThemForGenerate() {
	usage := &schema.TokenUsage{PromptTokens: 3, CompletionTokens: 2}
	step := steerlooptest.Step{
		Chunks:    []steerlooptest.Chunk{{Content: "ab"}, {Content: "cd", Delay: 5 * time.Millisecond}},
		ToolCalls: []schema.ToolCall{steerlooptest.ToolCall("c1", "lookup", `{}`)},
		Usage:     usage,
	}

	reader, err := steerlooptest.NewScriptedModel(step).Stream(context.Background(), userMessage("a"))
	s.Require().NoError(err)
	defer reader.Close()
	var chunks []*schema.Message
	for {
		chunk, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		s.Require().NoError(err)
		chunks = append(chunks, chunk)
	}
	s.Require().Len(chunks, 2)
	s.Empty(chunks[0].ToolCalls)
	s.Equal("lookup", chunks[1].ToolCalls[0].Function.Name, "tool calls ride on the last chunk")
	s.Equal(usage, chunks[1].ResponseMeta.Usage)

	msg, err := steerlooptest.NewScriptedModel(step).Generate(context.Background(), userMessage("a"))
	s.Require().NoError(err)
	s.Equal("abcd", msg.Content, "Generate joins the chunks")
	s.Equal(usage, msg.ResponseMeta.Usage)
}

func (s *LoopSuite) TestScriptedModel_AStreamStopsWhenTheContextIsCancelled() {
	m := steerlooptest.NewScriptedModel(steerlooptest.Step{
		Chunks: []steerlooptest.Chunk{{Content: "a"}, {Content: "b", Delay: time.Second}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	reader, err := m.Stream(ctx, userMessage("a"))
	s.Require().NoError(err)
	defer reader.Close()

	_, err = reader.Recv()
	s.Require().NoError(err)
	cancel()
	_, err = reader.Recv()
	s.ErrorIs(err, context.Canceled)
}

func (s *LoopSuite) TestScriptedModel_AStreamReportsAStepErrorAndSendsTextAsOneChunk() {
	m := steerlooptest.NewScriptedModel(fails(errors.New("nope")), says("whole"))

	_, err := m.Stream(context.Background(), userMessage("a"))
	s.EqualError(err, "nope")

	reader, err := m.Stream(context.Background(), userMessage("a"))
	s.Require().NoError(err)
	msg, err := reader.Recv()
	s.Require().NoError(err)
	s.Equal("whole", msg.Content)
}

func (s *LoopSuite) TestScriptedModel_AHandlerReplacesTheScript() {
	m := &steerlooptest.ScriptedModel{
		Handler: func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			if msgs[0].Content == "fail" {
				return nil, errors.New("handler failed")
			}
			return &schema.Message{Role: schema.Assistant, Content: "echo " + msgs[0].Content}, nil
		},
	}

	msg, err := m.Generate(context.Background(), userMessage("hi"))
	s.Require().NoError(err)
	s.Equal("echo hi", msg.Content)

	reader, err := m.Stream(context.Background(), userMessage("yo"))
	s.Require().NoError(err)
	chunk, err := reader.Recv()
	s.Require().NoError(err)
	s.Equal("echo yo", chunk.Content)

	_, err = m.Stream(context.Background(), userMessage("fail"))
	s.EqualError(err, "handler failed")
	s.Equal(3, m.CallCount())
}

func (s *LoopSuite) TestScriptedModel_RecordsOptionsAndBoundTools() {
	m := steerlooptest.NewScriptedModel(says("x"))
	bound, err := m.WithTools([]*schema.ToolInfo{{Name: "lookup"}})
	s.Require().NoError(err)
	s.Same(m, bound)
	s.Require().Len(m.BoundTools(), 1)
	s.Equal("lookup", m.BoundTools()[0][0].Name)

	_, err = m.Generate(context.Background(), userMessage("a"), model.WithTemperature(0.25), model.WithMaxTokens(9))
	s.Require().NoError(err)
	s.InDelta(0.25, *m.LastOptions().Temperature, 0.0001)
	s.Equal(9, *m.LastOptions().MaxTokens)
}

func (s *LoopSuite) TestRecordingExecutor_RecordsEveryCall() {
	out, err := s.exec.ExecuteTool(context.Background(), "lookup", `{"q":1}`)
	s.Require().NoError(err)
	s.Equal("result from lookup", out, "the default answer names the tool")

	failing := steerlooptest.NewRecordingExecutor(func(_ context.Context, name, args string) (string, error) {
		return "", errors.New(name + " failed with " + args)
	})
	_, err = failing.ExecuteTool(context.Background(), "a", "{}")
	s.EqualError(err, "a failed with {}")
	_, _ = failing.ExecuteTool(context.Background(), "b", "[]")
	s.Equal(2, failing.CallCount())
	s.Equal([]string{"a", "b"}, failing.CalledTools())
	s.Equal([]steerlooptest.ExecutedCall{{Name: "a", Args: "{}"}, {Name: "b", Args: "[]"}}, failing.Calls())
}

func (s *LoopSuite) TestScript_ParsesStepsFromJSON() {
	steps, err := steerlooptest.ParseScript([]byte(`{"steps":[
		{"tool_calls":[{"id":"c1","name":"lookup","arguments":{"q": "x"}},{"name":"ping"},{"name":"raw","arguments":"{\"a\":1}"}]},
		{"content":"answer","delay_ms":15,"usage":{"input":10,"output":4}},
		{"error":"rate limited"},
		{"chunks":[{"content":"a"},{"content":"b","delay_ms":7}]}
	]}`))
	s.Require().NoError(err)
	s.Require().Len(steps, 4)

	calls := steps[0].ToolCalls
	s.Require().Len(calls, 3)
	s.Equal("c1", calls[0].ID)
	s.Equal(`{"q":"x"}`, calls[0].Function.Arguments)
	s.Equal("call_1_2", calls[1].ID, "a missing id is generated")
	s.Equal("{}", calls[1].Function.Arguments)
	s.Equal(`{"a":1}`, calls[2].Function.Arguments)

	s.Equal("answer", steps[1].Content)
	s.Equal(15*time.Millisecond, steps[1].Delay)
	s.Equal(14, steps[1].Usage.TotalTokens)
	s.EqualError(steps[2].Err, "rate limited")
	s.Equal([]steerlooptest.Chunk{{Content: "a"}, {Content: "b", Delay: 7 * time.Millisecond}}, steps[3].Chunks)
}

func (s *LoopSuite) TestScript_RejectsMalformedInput() {
	for _, tc := range []struct{ name, in, want string }{
		{"not json", `{`, "parse script"},
		{"unknown field", `{"steps":[{"contnet":"typo"}]}`, "unknown field"},
		{"no steps", `{"steps":[]}`, "no steps"},
		{"unnamed tool", `{"steps":[{"tool_calls":[{"id":"x"}]}]}`, "name is required"},
		{"bad arg string", `{"steps":[{"tool_calls":[{"name":"x","arguments":"{oops"}]}]}`, "not valid JSON"},
	} {
		s.Run(tc.name, func() {
			_, err := steerlooptest.ParseScript([]byte(tc.in))
			s.ErrorContains(err, tc.want)
		})
	}
}

func (s *LoopSuite) TestScript_LoadsFromAFile() {
	path := filepath.Join(s.T().TempDir(), "s.json")
	s.Require().NoError(os.WriteFile(path, []byte(`{"steps":[{"content":"hi"}]}`), 0o600))
	steps, err := steerlooptest.LoadScript(path)
	s.Require().NoError(err)
	s.Equal("hi", steps[0].Content)

	_, err = steerlooptest.LoadScript(filepath.Join(s.T().TempDir(), "missing.json"))
	s.Error(err)

	s.Require().NoError(os.WriteFile(path, []byte(`{"steps":[]}`), 0o600))
	_, err = steerlooptest.LoadScript(path)
	s.ErrorContains(err, "s.json", "the error names the file")
}

// =============================================================================
// Fuzz targets. Go requires these to be top-level functions; CI runs them by name.
// =============================================================================

func FuzzNewToolInfo(f *testing.F) {
	f.Add("search", `{"type":"object","properties":{"q":{"type":"string"}}}`)
	f.Add("", `{}`)
	f.Add("x", `{"properties":7}`)
	f.Fuzz(func(t *testing.T, name, schemaJSON string) {
		var params map[string]any
		if err := json.Unmarshal([]byte(schemaJSON), &params); err != nil {
			return
		}
		_, _ = NewToolInfo(name, "desc", params) // must not panic
	})
}

func FuzzSerializeEinoMessagesForAudit(f *testing.F) {
	f.Add("user", "hello", "call", "tool", `{"a":1}`, 3)
	f.Add("", "", "", "", "", 0)
	f.Fuzz(func(t *testing.T, role, content, callID, toolName, args string, repeat int) {
		if repeat < 0 || repeat > 64 {
			return
		}
		msgs := []*schema.Message{nil}
		for range repeat + 1 {
			msgs = append(msgs, &schema.Message{
				Role:       schema.RoleType(role),
				Content:    content,
				ToolCallID: callID,
				ToolName:   toolName,
				ToolCalls:  []schema.ToolCall{steerlooptest.ToolCall(callID, toolName, args)},
			})
		}
		raw, _, _ := SerializeEinoMessagesForAudit(msgs)
		if !json.Valid(raw) {
			t.Fatalf("invalid JSON: %q", raw)
		}
		if len(raw) > maxAuditRequestJSONBytes {
			t.Fatalf("output %d bytes exceeds the cap", len(raw))
		}
		if !json.Valid(SerializeAssistantToolCalls(msgs[1])) {
			t.Fatal("invalid tool call JSON")
		}
	})
}
