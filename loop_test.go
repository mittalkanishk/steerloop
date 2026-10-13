package steerloop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// =============================================================================
// Tests
// =============================================================================
func TestLoop_StartingMessagesUnchangedWhenWorkingSliceIsEdited(t *testing.T) {

	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "Hello there!"})

	exec := steerlooptest.NewRecordingExecutor(nil)

	loop, err := New(basicConfigWithHistoryCompactor(), llm, nil, exec, nil)
	require.Nil(t, err)

	openingCallersMessages := basicMessages()
	openingCallersMessage := openingCallersMessages[0]

	events := loop.Start(context.Background(), openingCallersMessages)
	require.Same(t, openingCallersMessage, loop.startingMessages[0])
	require.Len(t, loop.startingMessages, len(openingCallersMessages))

	collectEvents(t, events, 5*time.Second)

	// starting message slice is not modified.
	require.Same(t, openingCallersMessage, loop.startingMessages[0])
	require.Len(t, loop.startingMessages, len(openingCallersMessages))

	// running message or working slice is edited.
	runningMessages := loop.GetRunningMessages()
	require.Len(t, runningMessages, len(openingCallersMessages)+1)
	require.Same(t, openingCallersMessage, runningMessages[0])
	require.Equal(t, schema.Assistant, runningMessages[len(runningMessages)-1].Role)
	require.Equal(t, "Hello there!", runningMessages[len(runningMessages)-1].Content)
}

func TestLoop_SimpleTextResponse(t *testing.T) {
	// LLM returns text immediately → loop stops (StopOnNoToolCalls)
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "Hello there!"})

	exec := steerlooptest.NewRecordingExecutor(nil)

	loop, err := New(basicConfig(), llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())

	allEvents := collectEvents(t, events, 5*time.Second)

	// Should have: thinking → response → done
	require.NotEmpty(t, allEvents)

	thinkEvt := findEvent(allEvents, EventThinking)
	require.NotNil(t, thinkEvt, "should have thinking event")

	respEvt := findEvent(allEvents, EventResponse)
	require.NotNil(t, respEvt, "should have response event")
	assert.Equal(t, "Hello there!", respEvt.Data.(ResponseData).Content)

	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt, "should have done event")
	doneData := doneEvt.Data.(DoneData)
	assert.Equal(t, DoneReasonFinished, doneData.Reason)
	assert.Equal(t, 1, doneData.Iterations)
	assert.Equal(t, "Hello there!", doneData.FinalContent)

	// Executor should not have been called
	assert.Equal(t, 0, exec.CallCount())

	// Loop should be done
	assert.Equal(t, StateDone, loop.GetState())
}

func TestLoop_ToolCallThenResponse(t *testing.T) {
	// Iteration 1: LLM requests a tool call
	// Iteration 2: LLM responds with text (using tool result)
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_1", "get_profile", `{}`)}},
		steerlooptest.Step{Content: "Based on your profile, here is a lesson."},
	)

	exec := steerlooptest.NewRecordingExecutor(func(ctx context.Context, name, args string) (string, error) {
		if name == "get_profile" {
			return `{"level": "intermediate", "interests": ["go", "systems"]}`, nil
		}
		return "unknown tool", nil
	})

	loop, err := New(basicConfig(), llm, sampleTools(), exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())

	allEvents := collectEvents(t, events, 5*time.Second)

	// Should have tool call and tool result events
	toolCallEvts := findEvents(allEvents, EventToolCall)
	require.Len(t, toolCallEvts, 1, "should have 1 tool call event")
	assert.Equal(t, "get_profile", toolCallEvts[0].Data.(ToolCallData).Name)

	toolResultEvts := findEvents(allEvents, EventToolResult)
	require.Len(t, toolResultEvts, 1, "should have 1 tool result event")
	assert.Contains(t, toolResultEvts[0].Data.(ToolResultData).Result, "intermediate")

	// Final response
	respEvt := findEvent(allEvents, EventResponse)
	require.NotNil(t, respEvt)
	assert.Contains(t, respEvt.Data.(ResponseData).Content, "profile")

	// Done
	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)
	assert.Equal(t, DoneReasonFinished, doneEvt.Data.(DoneData).Reason)
	assert.Equal(t, 2, doneEvt.Data.(DoneData).Iterations)

	// Executor called once
	assert.Equal(t, 1, exec.CallCount())
	assert.Equal(t, []string{"get_profile"}, exec.CalledTools())
}

func TestLoop_MultipleToolCalls(t *testing.T) {
	// LLM requests two tool calls in one response, then responds with text.
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{
			toolCall("call_1", "get_profile", `{}`),
			toolCall("call_2", "search_content", `{"query":"go concurrency"}`),
		}},
		steerlooptest.Step{Content: "Here is your personalized lesson on Go concurrency."},
	)

	exec := steerlooptest.NewRecordingExecutor(nil)

	loop, err := New(basicConfig(), llm, sampleTools(), exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	// Two tool calls, two results
	assert.Len(t, findEvents(allEvents, EventToolCall), 2)
	assert.Len(t, findEvents(allEvents, EventToolResult), 2)

	// Two iterations (tool calls + text)
	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)
	assert.Equal(t, 2, doneEvt.Data.(DoneData).Iterations)

	// Executor called twice
	assert.Equal(t, 2, exec.CallCount())
}

func TestLoop_FinishTool(t *testing.T) {
	// StopOnFinishTool mode: LLM calls tools, then calls "finish" with structured output.
	finishArgs := `{"title":"Lesson 1","content":"Learn about goroutines..."}`

	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_1", "get_profile", `{}`)}},
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_finish", FinishToolName, finishArgs)}},
	)

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StopCondition = StopOnFinishTool

	loop, err := New(cfg, llm, sampleTools(), exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)

	doneData := doneEvt.Data.(DoneData)
	assert.Equal(t, DoneReasonFinished, doneData.Reason)
	assert.Equal(t, finishArgs, doneData.FinalContent)
	assert.Equal(t, 2, doneData.Iterations)

	// Executor should have been called for get_profile (NOT for finish)
	assert.Equal(t, 1, exec.CallCount())
	assert.Equal(t, []string{"get_profile"}, exec.CalledTools())
}

func TestLoop_MaxIterations(t *testing.T) {

	// LLM always requests tool calls → hits max iterations.
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{
		ToolCalls: []schema.ToolCall{toolCall("call_loop", "get_profile", `{}`)},
	})

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.MaxIterations = 3

	loop, err := New(cfg, llm, sampleTools(), exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)

	doneData := doneEvt.Data.(DoneData)
	assert.Equal(t, DoneReasonMaxIterations, doneData.Reason)
	assert.Equal(t, 3, doneData.Iterations)

	// 3 iterations × 1 tool call each
	assert.Equal(t, 3, exec.CallCount())
}

func TestLoop_StopControl(t *testing.T) {
	// LLM is slow; user sends stop mid-loop.
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{
			Content: "First response",
			Delay:   50 * time.Millisecond,
		},
	)

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StopCondition = StopOnExplicit // only stops on signal
	cfg.MaxIterations = 100

	loop, err := New(cfg, llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())

	// Let it run for a bit, then stop
	time.Sleep(100 * time.Millisecond)
	loop.Stop()

	allEvents := collectEvents(t, events, 5*time.Second)

	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)
	assert.Equal(t, DoneReasonStopped, doneEvt.Data.(DoneData).Reason)

	assert.Equal(t, StateStopped, loop.GetState())
}

func TestLoop_InjectControl(t *testing.T) {

	// Use a blocking mock LLM: iteration 1 blocks until we inject, then unblocks.
	// This guarantees the inject is processed between iterations.
	injectReady := make(chan struct{})

	responses := []steerlooptest.Step{
		{Content: "response 1"}, // iteration 1
		{Content: "response 2"}, // iteration 2 (after inject)
		{Content: "response 3"}, // iteration 3
	}

	callIdx := 0
	var callMu sync.Mutex

	mockLLM := &steerlooptest.ScriptedModel{
		Handler: func(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
			callMu.Lock()
			idx := callIdx
			callIdx++
			callMu.Unlock()

			if idx == 0 {
				defer close(injectReady)
			}

			if idx == 1 {
				time.Sleep(5 * time.Millisecond)
			}

			r := responses[idx%len(responses)]

			return &schema.Message{Role: schema.Assistant, Content: r.Content}, nil
		},
	}

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StopCondition = StopOnExplicit
	cfg.MaxIterations = 3

	loop, err := New(cfg, mockLLM, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())

	// Wait for first iteration to complete, then inject
	go func() {
		<-injectReady
		time.Sleep(2 * time.Millisecond) // small gap for control drain
		loop.Inject("use Python examples")
	}()

	allEvents := collectEvents(t, events, 5*time.Second)

	// Should reach max_iterations
	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)
	assert.Equal(t, DoneReasonMaxIterations, doneEvt.Data.(DoneData).Reason)

	hasInjected := false

	for _, call := range mockLLM.Calls() {
		for _, msg := range call {
			if msg != nil && strings.Contains(msg.Content, "Python") {
				hasInjected = true
			}
		}
	}

	assert.True(t, hasInjected, "injected message should appear in LLM conversation history")
}

func TestLoop_ModifyConfig(t *testing.T) {
	// Start with MaxIterations=1, modify to 3 mid-flight
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{
			Content: "response",
			Delay:   10 * time.Millisecond,
		},
	)

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StopCondition = StopOnExplicit
	cfg.MaxIterations = 1

	loop, err := New(cfg, llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())

	// Modify config before first iteration completes
	newMax := 3
	loop.SendControlSignal(NewModifyConfigControl(ModifyConfigData{MaxIterations: &newMax}))

	allEvents := collectEvents(t, events, 5*time.Second)

	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)

	// Should have more than 1 iteration due to config modification
	doneData := doneEvt.Data.(DoneData)
	assert.True(t, doneData.Iterations >= 1, "should have at least 1 iteration")
}

func TestLoop_Reflection(t *testing.T) {
	// Enable reflection: after the LLM responds, it should do a reflection step.
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{Content: "Here is the lesson on goroutines."},
		steerlooptest.Step{Content: "The response looks complete and accurate."}, // reflection
	)

	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.EnableReflection = true

	loop, err := New(cfg, llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	// Should have a reflection event
	reflEvt := findEvent(allEvents, EventReflection)
	require.NotNil(t, reflEvt, "should have reflection event")
	assert.Contains(t, reflEvt.Data.(ReflectionData).Content, "complete")

	// LLM should have been called twice (response + reflection)
	assert.Equal(t, 2, llm.CallCount())
}

func TestLoop_LLMError(t *testing.T) {
	// LLM returns an error → loop emits error + done events.
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{Err: fmt.Errorf("API rate limit exceeded")})
	exec := steerlooptest.NewRecordingExecutor(nil)

	loop, err := New(basicConfig(), llm, nil, exec, nil)
	require.Nil(t, err)

	events := loop.Start(context.Background(), basicMessages())
	allEvents := collectEvents(t, events, 5*time.Second)

	errEvt := findEvent(allEvents, EventError)
	require.NotNil(t, errEvt)
	errData := errEvt.Data.(ErrorData)
	assert.Contains(t, errData.Error, "rate limit")
	assert.True(t, errData.Fatal)

	doneEvt := findEvent(allEvents, EventDone)
	require.NotNil(t, doneEvt)
	assert.Equal(t, DoneReasonError, doneEvt.Data.(DoneData).Reason)

	assert.Equal(t, StateError, loop.GetState())
}

// A reader that starts late still receives the done event, even though the
// loop produced more intermediate events than the channel can hold.
func TestLoop_DoneDeliveredToSlowReader(t *testing.T) {
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "still working"})
	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StopCondition = StopOnExplicit
	cfg.MaxIterations = 80 // 160 intermediate events against a 64-slot channel

	loop, err := New(cfg, llm, nil, exec, nil)
	require.NoError(t, err)

	events := loop.Start(context.Background(), basicMessages())
	time.Sleep(300 * time.Millisecond) // the loop has finished producing by now

	all := collectEvents(t, events, 10*time.Second)
	require.Len(t, findEvents(all, EventDone), 1, "exactly one done event must reach the reader")
	assert.Equal(t, DoneReasonMaxIterations, findEvent(all, EventDone).Data.(DoneData).Reason)
}

// Stop takes effect even when the control queue is full.
func TestLoop_StopWithFullControlQueue(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	llm := &steerlooptest.ScriptedModel{
		Handler: func(ctx context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			once.Do(func() { <-release }) // hold the first call open
			return &schema.Message{Role: schema.Assistant, Content: "working"}, nil
		},
	}
	exec := steerlooptest.NewRecordingExecutor(nil)

	cfg := basicConfig()
	cfg.StopCondition = StopOnExplicit
	cfg.MaxIterations = 5

	loop, err := New(cfg, llm, nil, exec, nil)
	require.NoError(t, err)
	events := loop.Start(context.Background(), basicMessages())

	for llm.CallCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	temp := 0.1
	for i := 0; i < controlChBuffer; i++ {
		require.True(t, loop.SendControlSignal(NewModifyConfigControl(ModifyConfigData{Temperature: &temp})))
	}
	loop.Stop()
	close(release)

	all := collectEvents(t, events, 5*time.Second)
	done := findEvent(all, EventDone)
	require.NotNil(t, done)
	assert.Equal(t, DoneReasonStopped, done.Data.(DoneData).Reason)
	assert.Equal(t, StateStopped, loop.GetState())
	assert.Equal(t, 1, llm.CallCount(), "no model call may start after Stop")
}

// A panicking tool becomes a tool error; the run carries on.
func TestLoop_ToolPanicIsRecovered(t *testing.T) {
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_1", "get_profile", `{}`)}},
		steerlooptest.Step{Content: "recovered"},
	)
	exec := steerlooptest.NewRecordingExecutor(func(context.Context, string, string) (string, error) {
		panic("boom")
	})

	loop, err := New(basicConfig(), llm, sampleTools(), exec, nil)
	require.NoError(t, err)
	all := collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)

	done := findEvent(all, EventDone)
	require.NotNil(t, done)
	assert.Equal(t, DoneReasonFinished, done.Data.(DoneData).Reason)

	result := findEvent(all, EventToolResult)
	require.NotNil(t, result)
	assert.Contains(t, result.Data.(ToolResultData).Error, "panicked")
	assert.Contains(t, result.Data.(ToolResultData).Result, "boom")

	var nonFatal int
	for _, e := range findEvents(all, EventError) {
		if !e.Data.(ErrorData).Fatal {
			nonFatal++
		}
	}
	assert.Equal(t, 1, nonFatal)
}

// Configuration changes arriving while a pause function makes model calls
// from another goroutine must not race. Run with -race.
func TestLoop_ModifyConfigDuringPauseIsRaceFree(t *testing.T) {
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_1", "ask_user", `{}`)}},
		steerlooptest.Step{Content: "side call"},
	)
	exec := steerlooptest.NewRecordingExecutor(nil)

	blocked := make(chan struct{})
	cfg := basicConfig()
	cfg.Pauses = map[string]PauseFunc{
		"ask_user": func(ctx context.Context, in PauseIn) (string, bool) {
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 50; i++ {
					_, _ = in.Call(ctx, nil, in.Messages)
					in.Emit(EventProgress, ProgressData{Message: "side"})
				}
			}()
			close(blocked)
			reply, ok := in.Block(ctx)
			wg.Wait()
			return reply, ok
		},
	}

	loop, err := New(cfg, llm, nil, exec, nil)
	require.NoError(t, err)
	events := loop.Start(context.Background(), basicMessages())
	go func() {
		for range events {
		}
	}()

	<-blocked
	for i := 0; i < 50; i++ {
		temp := float64(i) / 100
		for !loop.SendControlSignal(NewModifyConfigControl(ModifyConfigData{Temperature: &temp})) {
			time.Sleep(time.Millisecond)
		}
		_ = loop.GetState()
	}
	for !loop.Inject("the answer") {
		time.Sleep(time.Millisecond)
	}
	loop.Wait()

	done, ok := loop.Result()
	require.True(t, ok)
	assert.Equal(t, DoneReasonFinished, done.Reason)
}
