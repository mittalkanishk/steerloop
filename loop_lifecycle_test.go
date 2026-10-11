package steerloop

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Kanishkmittal55/steerloop/steerlooptest"
)

func TestLoop_ToolExecutionError(t *testing.T) {
	// Tool returns an error → loop feeds it back to LLM and continues.
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_1", "get_profile", `{}`)}},
		steerlooptest.Step{Content: "I couldn't access your profile, but here's a general lesson."},
	)

	exec := steerlooptest.NewRecordingExecutor(func(ctx context.Context, name, args string) (string, error) {
		return "", fmt.Errorf("database connection failed")
	})

	loop, err := New(basicConfig(), llm, sampleTools(), exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	// Tool result event should contain the error
	toolResultEvts := findEvents(allEvents, EventToolResult)
	require.Len(t, toolResultEvts, 1)
	resultData := toolResultEvts[0].Data.(ToolResultData)
	assert.Contains(t, resultData.Error, "database connection failed")

	// Loop should still complete (errors are not fatal to the loop)
	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)
	assert.Equal(t, DoneReasonFinished, doneEvt.Data.(DoneData).Reason)
}

func TestLoop_TotalTimeout(t *testing.T) {
	// LLM is slow, total timeout fires.
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{
		Content: "slow response",
		Delay:   500 * time.Millisecond,
	})

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.TotalTimeout = 100 * time.Millisecond
	cfg.StopCondition = StopOnExplicit
	cfg.MaxIterations = 100

	loop, err := New(cfg, llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)

	// Could be timeout or error (context deadline exceeded)
	doneData := doneEvt.Data.(DoneData)
	assert.True(t,
		doneData.Reason == DoneReasonTimeout || doneData.Reason == DoneReasonError,
		"should be timeout or error, got: %s", doneData.Reason,
	)
}

func TestLoop_StepTimeout(t *testing.T) {
	// Step timeout is short, LLM is slow.
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{
		Content: "slow response",
		Delay:   500 * time.Millisecond,
	})

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StepTimeout = 50 * time.Millisecond
	cfg.TotalTimeout = 2 * time.Second

	loop, err := New(cfg, llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	// Should get an error event (context deadline exceeded from step timeout)
	errEvt := findEvent(allEvents, EventError)
	require.NotNil(t, errEvt, "should have error from step timeout")
	assert.True(t, errEvt.Data.(ErrorData).Fatal)
}

func TestLoop_StreamEventsDisabled(t *testing.T) {
	// When StreamEvents=false, only Done and Error events should be emitted.
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_1", "get_profile", `{}`)}},
		steerlooptest.Step{Content: "Here is the lesson."},
	)

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StreamEvents = false

	loop, err := New(cfg, llm, sampleTools(), exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	// Only done event should be present (no thinking, tool_call, response, etc.)
	for _, evt := range allEvents {
		assert.True(t,
			evt.Type == EventDone || evt.Type == EventError,
			"unexpected event type when streaming is disabled: %s", evt.Type,
		)
	}

	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)
}

func TestLoop_Wait(t *testing.T) {
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "done", Delay: 50 * time.Millisecond})
	exec := steerlooptest.NewRecordingExecutor(nil)

	loop, err := New(basicConfig(), llm, nil, exec, nil)
	require.Nil(t, err)

	loop.Start(context.Background(), basicMessages())

	// Drain events in background
	go func() {
		for range loop.Events() {
		}
	}()

	// Wait should block until done
	done := make(chan struct{})
	go func() {
		loop.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Wait() blocked for too long")
	}

	assert.Equal(t, StateDone, loop.GetState())
}

func TestLoop_DoubleStart(t *testing.T) {
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "hello"})
	exec := steerlooptest.NewRecordingExecutor(nil)

	loop, err := New(basicConfig(), llm, nil, exec, nil)
	require.Nil(t, err)

	// First start
	events1 := loop.Start(context.Background(), basicMessages())

	// Second start should return a separate error channel
	events2 := loop.Start(context.Background(), basicMessages())

	// events2 should have an error and be a different channel
	assert.NotEqual(t, fmt.Sprintf("%p", events1), fmt.Sprintf("%p", events2),
		"second Start() should return a different channel")

	var gotError bool
	for evt := range events2 {
		if evt.Type == EventError {
			assert.Contains(t, evt.Data.(ErrorData).Error, "already started")
			gotError = true
		}
	}
	assert.True(t, gotError, "second Start() should emit an error")

	// Drain events1 and wait for the original loop to finish
	go func() {
		for range events1 {
		}
	}()
	loop.Wait()
}

func TestLoop_ContextCancellation(t *testing.T) {
	// Parent context is cancelled externally.
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{
		Content: "response",
		Delay:   200 * time.Millisecond,
	})

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StopCondition = StopOnExplicit
	cfg.MaxIterations = 100

	ctx, cancel := context.WithCancel(context.Background())

	loop, err := New(cfg, llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(ctx, basicMessages())

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	allEvents := collectEvents(t, events, 5*time.Second)

	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)
}

func TestLoop_FinishToolWithTextResponse(t *testing.T) {
	// StopOnFinishTool mode: LLM first responds with text (no finish), then with finish.
	finishArgs := `{"quiz": [{"q": "What is a goroutine?", "a": "A lightweight thread"}]}`

	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{Content: "Let me think about this..."}, // text, not finish
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_finish", FinishToolName, finishArgs)}},
	)

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StopCondition = StopOnFinishTool

	loop, err := New(cfg, llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)

	doneData := doneEvt.Data.(DoneData)
	assert.Equal(t, DoneReasonFinished, doneData.Reason)
	assert.Equal(t, finishArgs, doneData.FinalContent)
	assert.Equal(t, 2, doneData.Iterations)
}

func TestLoop_EmptyLLMResponse(t *testing.T) {

	emptyLLM := &steerlooptest.ScriptedModel{
		Handler: func(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			return &schema.Message{}, nil
		},
	}

	exec := steerlooptest.NewRecordingExecutor(nil)

	loop, err := New(basicConfig(), emptyLLM, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())

	allEvents := collectEvents(t, events, 5*time.Second)

	errEvt := findEvent(allEvents, EventError)

	require.NotNil(t, errEvt)
	assert.Contains(t, errEvt.Data.(ErrorData).Error, "empty LLM response")
	assert.True(t, errEvt.Data.(ErrorData).Fatal)
}

func TestLoop_EventTimestamps(t *testing.T) {
	before := time.Now().UTC()

	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "hello"})
	exec := steerlooptest.NewRecordingExecutor(nil)

	loop, err := New(basicConfig(), llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	after := time.Now().UTC()

	for _, evt := range allEvents {
		assert.False(t, evt.Timestamp.IsZero(), "event timestamp should not be zero")
		assert.True(t, !evt.Timestamp.Before(before), "event timestamp should be after start")
		assert.True(t, !evt.Timestamp.After(after), "event timestamp should be before end")
	}
}

func TestLoop_EventIterationNumbers(t *testing.T) {
	// Two iterations (tool call + response).
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_1", "get_profile", `{}`)}},
		steerlooptest.Step{Content: "Response after tools."},
	)

	exec := steerlooptest.NewRecordingExecutor(nil)

	loop, err := New(basicConfig(), llm, sampleTools(), exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	// First thinking event should be iteration 1
	thinkEvts := findEvents(allEvents, EventThinking)
	require.True(t, len(thinkEvts) >= 2, "should have at least 2 thinking events")
	assert.Equal(t, 1, thinkEvts[0].Iteration)
	assert.Equal(t, 2, thinkEvts[1].Iteration)

	// Done should be iteration 2
	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)
	assert.Equal(t, 2, doneEvt.Iteration)
}

func TestLoop_ConversationHistory(t *testing.T) {
	// Verify that tool results are properly added to conversation history.
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_1", "search_content", `{"query":"go channels"}`)}},
		steerlooptest.Step{Content: "Based on the search results..."},
	)

	exec := steerlooptest.NewRecordingExecutor(func(ctx context.Context, name, args string) (string, error) {
		return "Go channels are typed conduits for communication between goroutines.", nil
	})

	loop, err := New(basicConfig(), llm, sampleTools(), exec, nil)
	require.Nil(t, err)

	loop.Start(context.Background(), basicMessages())
	loop.Wait()

	// Verify the second LLM call includes the tool result
	lastMsgs := llm.LastMessages()
	require.True(t, len(lastMsgs) > 3, "should have system + human + assistant(tool) + tool_result + messages")

	// Find the tool result message
	var foundToolResult bool
	for _, msg := range lastMsgs {
		if msg != nil && msg.Role == schema.Tool {
			assert.Equal(t, "call_1", msg.ToolCallID)
			assert.Equal(t, "search_content", msg.ToolName)
			assert.Contains(t, msg.Content, "Go channels")
			foundToolResult = true
		}
	}
	assert.True(t, foundToolResult, "tool result should be in conversation history")
}

func TestLoop_AllLLMCallsFlowThroughInjectedModel(t *testing.T) {
	// Every model call in the loop goes through the injected model. A tracking
	// wrapper counts all Generate calls; the count must equal the number of
	// model invocations, proving there is no bypass path.
	//
	// Scenario: tool call → response → reflection = 3 GenerateContent calls total.

	innerLLM := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_1", "get_profile", `{}`)}},
		steerlooptest.Step{Content: "Here is a lesson based on your profile."},
		steerlooptest.Step{Content: "The response is complete and accurate."},
	)

	var trackingCalls int
	var trackingMu sync.Mutex
	wrapper := &steerlooptest.ScriptedModel{
		Handler: func(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			trackingMu.Lock()
			trackingCalls++
			trackingMu.Unlock()
			return innerLLM.Generate(ctx, msgs, opts...)
		},
	}

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.EnableReflection = true

	loop, err := New(cfg, wrapper, sampleTools(), exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	collectEvents(t, events, 5*time.Second)

	// All 3 LLM calls (tool-call iteration + response iteration + reflection)
	// must have gone through the wrapper.
	trackingMu.Lock()
	finalCount := trackingCalls
	trackingMu.Unlock()

	assert.Equal(t, 3, finalCount,
		"all LLM calls (including reflection) must flow through the injected model — "+
			"a wrapping model must see every call for logging, metrics and cost")
}

func TestLoop_ConcurrentAccess(t *testing.T) {
	// Verify thread safety of State(), Iteration(), etc.
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{
		Content: "response",
		Delay:   5 * time.Millisecond,
	})

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StopCondition = StopOnExplicit
	cfg.MaxIterations = 5

	loop, err := New(cfg, llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())

	// Concurrent reads
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = loop.GetState()
			_ = loop.GetCurrentIteration()
		}()
	}

	// Drain events
	go func() {
		for range events {
		}
	}()

	wg.Wait()
	loop.Wait()

	// If we get here without panic or deadlock, test passes
}

func TestLogRunningMessages_SeedThenDiff(t *testing.T) {
	logs := &captureHandler{level: slog.LevelInfo}
	loop := &Loop{
		cfg:    LoopConfig{TranscriptLogLevel: slog.LevelInfo},
		logger: slog.New(logs),
		runningMessages: []*schema.Message{
			{Role: schema.System, Content: "sys"},
			{Role: schema.User, Content: "hi"},
		},
	}
	assert.Equal(t, "0 system sys\n1 user hi", formatTranscript(0, loop.runningMessages))
	loop.logged = len(loop.runningMessages)

	ctx := context.Background()
	loop.appendRunningMessages(&schema.Message{Role: schema.Assistant, Content: "looked"})
	loop.logRunningMessages(ctx, 1)
	loop.appendRunningMessages(&schema.Message{Role: schema.Tool, Content: "outline"})
	loop.logRunningMessages(ctx, 2)
	loop.logRunningMessages(ctx, 3)

	entries := logs.all()
	require.Len(t, entries, 2)
	assert.Equal(t, "agent loop diff", entries[0].msg)
	assert.Equal(t, "2 assistant looked", entries[0].attrs["diff"])
	assert.Equal(t, "3 tool outline", entries[1].attrs["diff"])
	assert.Equal(t, 4, loop.logged)
}
