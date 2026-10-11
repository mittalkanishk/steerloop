package steerloop

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mittalkanishk/steerloop/steerlooptest"
)

func TestNew_Validation(t *testing.T) {
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "x"})
	exec := steerlooptest.NewRecordingExecutor(nil)

	_, err := New(basicConfig(), llm, nil, nil, nil)
	assert.ErrorContains(t, err, "tool executor")
	_, err = New(basicConfig(), nil, nil, exec, nil)
	assert.ErrorContains(t, err, "LLM")

	_, err = New(basicConfig(), failingBinder{err: errors.New("no tools here")}, sampleTools(), exec, nil)
	assert.ErrorContains(t, err, "no tools here")
	_, err = New(basicConfig(), failingBinder{}, sampleTools(), exec, nil)
	assert.ErrorContains(t, err, "returned no model")

	loop, err := New(basicConfig(), llm, sampleTools(), exec, nil)
	require.NoError(t, err)
	require.NotNil(t, loop)
	require.Len(t, llm.BoundTools(), 1)
	assert.Len(t, llm.BoundTools()[0], 2)
}

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

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, 12, cfg.MaxIterations)
	assert.Equal(t, StopOnNoToolCalls, cfg.StopCondition)
	assert.True(t, cfg.StreamEvents)
	assert.Equal(t, time.Minute, cfg.StepTimeout)

	all, _ := runSteps(t, cfg, steerlooptest.Step{Content: "hi"})
	assert.Equal(t, "hi", doneOf(t, all).FinalContent)
}

func TestStopConditionLabel(t *testing.T) {
	assert.Equal(t, "no_tool_calls", StopConditionLabel(StopOnNoToolCalls))
	assert.Equal(t, "finish_tool", StopConditionLabel(StopOnFinishTool))
	assert.Equal(t, "explicit", StopConditionLabel(StopOnExplicit))
	assert.Equal(t, "", StopConditionLabel(StopCondition(99)))
}

func TestResult(t *testing.T) {
	release := make(chan struct{})
	llm := &steerlooptest.ScriptedModel{
		Handler: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			<-release
			return &schema.Message{Role: schema.Assistant, Content: "final"}, nil
		},
	}
	loop, err := New(basicConfig(), llm, nil, steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)

	_, ok := loop.Result()
	assert.False(t, ok, "no result before Start")
	events := loop.Start(context.Background(), basicMessages())
	_, ok = loop.Result()
	assert.False(t, ok, "no result while running")

	close(release)
	collectEvents(t, events, 5*time.Second)
	loop.Wait()
	done, ok := loop.Result()
	require.True(t, ok)
	assert.Equal(t, DoneReasonFinished, done.Reason)
	assert.Equal(t, "final", done.FinalContent)
	assert.Equal(t, 1, done.Iterations)
}

// A loop nobody reads still ends, and reports how many events it dropped.
func TestResult_UnreadLoopCountsDroppedEvents(t *testing.T) {
	cfg := basicConfig()
	cfg.StopCondition = StopOnExplicit
	cfg.MaxIterations = 20 // 40 intermediate events
	cfg.EventBuffer = 8
	cfg.DoneDeliveryGrace = 30 * time.Millisecond

	logs := &captureHandler{}
	llm := steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "tick"})
	loop, err := New(cfg, llm, nil, steerlooptest.NewRecordingExecutor(nil), slogNew(logs))
	require.NoError(t, err)
	loop.Start(context.Background(), basicMessages())
	loop.Wait()

	done, ok := loop.Result()
	require.True(t, ok)
	assert.Equal(t, DoneReasonMaxIterations, done.Reason)
	assert.Equal(t, 32, done.DroppedEvents)
	assert.Equal(t, 32, logs.count("agent loop event channel full, dropping event"))
	assert.Equal(t, 1, logs.count("agent loop done event not read within grace period; use Result"))
}

func TestEventSink_SeesEveryEventInOrder(t *testing.T) {
	var mu sync.Mutex
	var sunk []EventType
	cfg := basicConfig()
	cfg.EventSink = func(_ context.Context, evt Event) {
		mu.Lock()
		sunk = append(sunk, evt.Type)
		mu.Unlock()
	}
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("c1", "get_profile", `{}`)}},
		steerlooptest.Step{Content: "done"},
	)
	loop, err := New(cfg, llm, sampleTools(), steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)
	all := collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)

	var fromChannel []EventType
	for _, e := range all {
		fromChannel = append(fromChannel, e.Type)
	}
	assert.Equal(t, fromChannel, sunk)
	assert.Equal(t, []EventType{
		EventThinking, EventLLMTurn, EventToolCall, EventToolResult,
		EventThinking, EventResponse, EventDone,
	}, sunk)
}

func TestEventSink_QuietModeOnlyTerminalEvents(t *testing.T) {
	var sunk []EventType
	cfg := basicConfig()
	cfg.StreamEvents = false
	cfg.EventSink = func(_ context.Context, evt Event) { sunk = append(sunk, evt.Type) }
	all, _ := runSteps(t, cfg, steerlooptest.Step{Content: "hi"})
	assert.Len(t, all, 1)
	assert.Equal(t, []EventType{EventDone}, sunk)
}

func TestHooks_CallOptions(t *testing.T) {
	cfg := basicConfig()
	cfg.Temperature = 0.3
	all, llm := runSteps(t, cfg, steerlooptest.Step{Content: "hi"})
	require.NotNil(t, doneOf(t, all))
	require.NotNil(t, llm.LastOptions().Temperature)
	assert.InDelta(t, 0.3, *llm.LastOptions().Temperature, 0.001, "default is model.WithTemperature")

	var got float32
	cfg.CallOptions = func(temperature float32) []model.Option {
		got = temperature
		return []model.Option{model.WithMaxTokens(77)}
	}
	_, llm = runSteps(t, cfg, steerlooptest.Step{Content: "hi"})
	assert.InDelta(t, 0.3, got, 0.001)
	require.NotNil(t, llm.LastOptions().MaxTokens)
	assert.Equal(t, 77, *llm.LastOptions().MaxTokens)
	assert.Nil(t, llm.LastOptions().Temperature, "the hook replaces the default options")
}

func TestHooks_ModelIdentityAndCostOnLLMTurn(t *testing.T) {
	steps := []steerlooptest.Step{
		{
			ToolCalls: []schema.ToolCall{toolCall("c1", "get_profile", `{}`)},
			Usage:     &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 20},
		},
		{Content: "answer", Usage: &schema.TokenUsage{PromptTokens: 150, CompletionTokens: 30}},
	}
	run := func(cfg LoopConfig) ([]Event, DoneData) {
		llm := steerlooptest.NewScriptedModel(steps...)
		loop, err := New(cfg, llm, sampleTools(), steerlooptest.NewRecordingExecutor(nil), nil)
		require.NoError(t, err)
		all := collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)
		return all, doneOf(t, all)
	}

	all, done := run(basicConfig())
	turn := findEvent(all, EventLLMTurn).Data.(LLMTurnData)
	assert.Equal(t, 100, turn.InputTokens)
	assert.Equal(t, 20, turn.OutputTokens)
	assert.Zero(t, turn.CostAmount)
	assert.Empty(t, turn.Provider)
	assert.Equal(t, "tool_calls", turn.ResponseKind)
	assert.Equal(t, "no_tool_calls", turn.StopCondition)
	assert.Equal(t, 10, turn.MaxIterations)
	assert.Equal(t, 1, turn.TurnIndex)
	assert.JSONEq(t, `[{"id":"c1","type":"function","function":{"name":"get_profile","arguments":"{}"}}]`, string(turn.AssistantToolCallsJSON))
	var req struct {
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(turn.RequestMessagesJSON, &req))
	require.Len(t, req.Messages, 2)
	assert.Equal(t, "user", req.Messages[1].Role)
	assert.Equal(t, LLMUsageSummary{InputTokens: 250, OutputTokens: 50, TotalTokens: 300}, done.LLMUsage)

	cfg := basicConfig()
	cfg.ModelIdentity = func() (string, string) { return "acme", "big-1" }
	cfg.CostEstimator = func(provider, modelName string, in, out int) float64 {
		assert.Equal(t, "acme", provider)
		assert.Equal(t, "big-1", modelName)
		return float64(in)*0.01 + float64(out)*0.1
	}
	all, _ = run(cfg)
	turn = findEvent(all, EventLLMTurn).Data.(LLMTurnData)
	assert.Equal(t, "acme", turn.Provider)
	assert.Equal(t, "big-1", turn.Model)
	assert.InDelta(t, 3.0, turn.CostAmount, 0.0001)
}

func TestUsage_TotalOnlyProviders(t *testing.T) {
	all, _ := runSteps(t, basicConfig(), steerlooptest.Step{Content: "hi", Usage: &schema.TokenUsage{TotalTokens: 42}})
	assert.Equal(t, LLMUsageSummary{TotalTokens: 42}, doneOf(t, all).LLMUsage)
}

func TestHistoryCompactor_ShapesModelInputOnly(t *testing.T) {
	cfg := basicConfig()
	cfg.HistoryCompactor = func(msgs []*schema.Message) []*schema.Message {
		return msgs[len(msgs)-1:] // keep only the newest message
	}
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("c1", "get_profile", `{}`)}},
		steerlooptest.Step{Content: "answer"},
	)
	loop, err := New(cfg, llm, sampleTools(), steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)
	collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)

	for _, call := range llm.Calls() {
		assert.Len(t, call, 1, "the model sees the compacted history")
	}
	assert.Equal(t, schema.Tool, llm.LastMessages()[0].Role)
	assert.Len(t, loop.GetRunningMessages(), 5, "the stored history is not compacted")
}

func TestReflection_PromptIsConfigurable(t *testing.T) {
	steps := []steerlooptest.Step{{Content: "the answer"}, {Content: "looks fine"}}

	cfg := basicConfig()
	cfg.EnableReflection = true
	_, llm := runSteps(t, cfg, steps...)
	last := llm.LastMessages()
	assert.Equal(t, defaultReflectionPrompt+"\n\nYour response was:\nthe answer", last[len(last)-1].Content)

	cfg.ReflectionPrompt = "Check this for a beginner."
	all, llm := runSteps(t, cfg, steps...)
	last = llm.LastMessages()
	assert.Equal(t, "Check this for a beginner.\n\nYour response was:\nthe answer", last[len(last)-1].Content)
	assert.Equal(t, "looks fine", findEvent(all, EventReflection).Data.(ReflectionData).Content)
	assert.Equal(t, "the answer", doneOf(t, all).FinalContent)
}

func TestReflection_FailureIsNotFatal(t *testing.T) {
	cfg := basicConfig()
	cfg.EnableReflection = true
	cfg.RetryMaxAttempts = -1
	all, _ := runSteps(t, cfg, steerlooptest.Step{Content: "the answer"}, steerlooptest.Step{Err: errors.New("quota")})
	assert.Equal(t, DoneReasonFinished, doneOf(t, all).Reason)
	errEvt := findEvent(all, EventError)
	require.NotNil(t, errEvt)
	assert.False(t, errEvt.Data.(ErrorData).Fatal)
	assert.Contains(t, errEvt.Data.(ErrorData).Error, "reflection failed")
}

func TestModifyConfig_RaisesIterationCapAndTogglesReflection(t *testing.T) {
	gate := make(chan struct{})
	calls := 0
	llm := &steerlooptest.ScriptedModel{
		Handler: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			calls++
			if calls == 1 {
				<-gate
			}
			return &schema.Message{Role: schema.Assistant, Content: "tick"}, nil
		},
	}
	cfg := basicConfig()
	cfg.StopCondition = StopOnExplicit
	cfg.MaxIterations = 2
	loop, err := New(cfg, llm, nil, steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)
	events := loop.Start(context.Background(), basicMessages())

	five, on := 5, true
	require.True(t, loop.SendControlSignal(NewModifyConfigControl(ModifyConfigData{MaxIterations: &five, EnableReflection: &on})))
	close(gate)
	all := collectEvents(t, events, 5*time.Second)

	done := doneOf(t, all)
	assert.Equal(t, DoneReasonMaxIterations, done.Reason)
	assert.Equal(t, 5, done.Iterations)
	assert.True(t, loop.reflectionEnabled())
	var steps []string
	for _, e := range findEvents(all, EventProgress) {
		steps = append(steps, e.Data.(ProgressData).Step)
	}
	assert.Equal(t, []string{"config_modify"}, steps)
}

func TestControl_EmptyInjectIsIgnoredAndUnknownTypeIsHarmless(t *testing.T) {
	gate := make(chan struct{})
	first := true
	llm := &steerlooptest.ScriptedModel{
		Handler: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			if first {
				first = false
				<-gate
			}
			return &schema.Message{Role: schema.Assistant, Content: "tick"}, nil
		},
	}
	cfg := basicConfig()
	cfg.StopCondition = StopOnExplicit
	cfg.MaxIterations = 2
	loop, err := New(cfg, llm, nil, steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)
	events := loop.Start(context.Background(), basicMessages())
	require.True(t, loop.Inject(""))
	require.True(t, loop.SendControlSignal(Control{Type: "unknown"}))
	require.True(t, loop.SendControlSignal(Control{Type: ControlModifyConfig, Data: "wrong payload"}))
	close(gate)
	all := collectEvents(t, events, 5*time.Second)

	assert.Empty(t, findEvents(all, EventProgress))
	assert.Len(t, loop.GetRunningMessages(), 4, "two seed messages and two replies; nothing injected")
}

func TestControl_QueueFullIsReported(t *testing.T) {
	loop, err := New(basicConfig(), steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "x"}), nil, steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)
	for i := 0; i < controlChBuffer; i++ {
		require.True(t, loop.Inject("m"))
	}
	assert.False(t, loop.Inject("one too many"))
	assert.False(t, loop.SendControlSignal(NewStopControl()))
	loop.Stop()
	loop.Stop() // idempotent
	assert.NotNil(t, loop.Events())
}

func TestAutoNudge(t *testing.T) {
	finish := steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("f", FinishToolName, `{"ok":true}`)}}
	nudgeSeen := func(llm *steerlooptest.ScriptedModel) string {
		last := llm.LastMessages()
		return last[len(last)-1].Content
	}

	cfg := basicConfig()
	cfg.StopCondition = StopOnFinishTool
	all, llm := runSteps(t, cfg, steerlooptest.Step{Content: "thinking aloud"}, finish)
	assert.Equal(t, `{"ok":true}`, doneOf(t, all).FinalContent)
	assert.Equal(t, defaultAutoNudge, nudgeSeen(llm))

	cfg.AutoNudgeText = "Use a tool."
	_, llm = runSteps(t, cfg, steerlooptest.Step{Content: "thinking aloud"}, finish)
	assert.Equal(t, "Use a tool.", nudgeSeen(llm))

	cfg.DisableAutoNudge = true
	_, llm = runSteps(t, cfg, steerlooptest.Step{Content: "thinking aloud"}, finish)
	assert.Equal(t, "thinking aloud", nudgeSeen(llm), "no synthetic user message is added")
}

func TestTranscriptLogLevel(t *testing.T) {
	run := func(handlerLevel, transcriptLevel int) *captureHandler {
		logs := &captureHandler{level: slogLevel(handlerLevel)}
		cfg := basicConfig()
		cfg.TranscriptLogLevel = slogLevel(transcriptLevel)
		loop, err := New(cfg, steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "hi"}), nil,
			steerlooptest.NewRecordingExecutor(nil), slogNew(logs))
		require.NoError(t, err)
		collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)
		return logs
	}
	quiet := run(0, -4) // handler at info, transcript at debug
	assert.Zero(t, quiet.count("agent loop seed"))
	loud := run(0, 0) // both at info
	assert.Equal(t, 1, loud.count("agent loop seed"))
	assert.Equal(t, 1, loud.count("agent loop diff"))
}

func TestToolExecutorFunc(t *testing.T) {
	var exec ToolExecutor = ToolExecutorFunc(func(_ context.Context, name, args string) (string, error) {
		return name + ":" + args, nil
	})
	out, err := exec.ExecuteTool(context.Background(), "echo", "{}")
	require.NoError(t, err)
	assert.Equal(t, "echo:{}", out)
}
