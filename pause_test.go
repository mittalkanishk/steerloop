package steerloop

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// pauseLoop builds a loop whose first model turn calls the "ask_user" pause
// tool and whose second turn answers in text.
func pauseLoop(t *testing.T, pause PauseFunc) (*Loop, *steerlooptest.ScriptedModel, *steerlooptest.RecordingExecutor) {
	t.Helper()
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{toolCall("call_1", "ask_user", `{"question":"colour?"}`)}},
		steerlooptest.Step{Content: "thanks"},
	)
	exec := steerlooptest.NewRecordingExecutor(nil)
	cfg := basicConfig()
	cfg.Pauses = map[string]PauseFunc{"ask_user": pause}
	loop, err := New(cfg, llm, nil, exec, nil)
	require.NoError(t, err)
	return loop, llm, exec
}

func doneOf(t *testing.T, events []Event) DoneData {
	t.Helper()
	dones := findEvents(events, EventDone)
	require.Len(t, dones, 1, "a run has exactly one done event")
	return dones[0].Data.(DoneData)
}

func TestPause_ReplyBecomesToolResult(t *testing.T) {
	var seen PauseIn
	loop, llm, exec := pauseLoop(t, func(_ context.Context, in PauseIn) (string, bool) {
		seen = in
		return "blue", true
	})
	all := collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)

	assert.Equal(t, DoneReasonFinished, doneOf(t, all).Reason)
	assert.Equal(t, 0, exec.CallCount(), "a pause tool never reaches the executor")
	assert.Equal(t, "call_1", seen.CallID)
	assert.Equal(t, "ask_user", seen.Name)
	assert.JSONEq(t, `{"question":"colour?"}`, seen.Arguments)
	assert.Len(t, seen.Messages, 3, "seed messages plus the assistant tool call")

	result := findEvent(all, EventToolResult).Data.(ToolResultData)
	assert.Equal(t, "blue", result.Result)

	last := llm.LastMessages()
	tool := last[len(last)-1]
	assert.Equal(t, schema.Tool, tool.Role)
	assert.Equal(t, "blue", tool.Content)
	assert.Equal(t, "call_1", tool.ToolCallID)
}

func TestPause_EmptyReplyIsFilledIn(t *testing.T) {
	loop, llm, _ := pauseLoop(t, func(context.Context, PauseIn) (string, bool) { return "", true })
	collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)
	last := llm.LastMessages()
	assert.Equal(t, "(no response)", last[len(last)-1].Content)
}

func TestPause_BlockReceivesInject(t *testing.T) {
	blocked := make(chan struct{})
	loop, llm, _ := pauseLoop(t, func(ctx context.Context, in PauseIn) (string, bool) {
		in.Emit(EventAwaitingInput, AwaitingInputData{Question: "colour?"})
		close(blocked)
		return in.Block(ctx)
	})
	events := loop.Start(context.Background(), basicMessages())
	<-blocked
	require.True(t, loop.Inject("green"))

	all := collectEvents(t, events, 5*time.Second)
	assert.Equal(t, DoneReasonFinished, doneOf(t, all).Reason)
	require.NotNil(t, findEvent(all, EventAwaitingInput))
	last := llm.LastMessages()
	assert.Equal(t, "green", last[len(last)-1].Content)
}

func TestPause_StopWhileBlocked(t *testing.T) {
	blocked := make(chan struct{})
	loop, llm, _ := pauseLoop(t, func(ctx context.Context, in PauseIn) (string, bool) {
		close(blocked)
		return in.Block(ctx)
	})
	events := loop.Start(context.Background(), basicMessages())
	<-blocked
	loop.Stop()

	all := collectEvents(t, events, 5*time.Second)
	assert.Equal(t, DoneReasonStopped, doneOf(t, all).Reason)
	assert.Equal(t, StateStopped, loop.GetState())
	assert.Equal(t, 1, llm.CallCount())
}

func TestPause_StopControlWhileBlocked(t *testing.T) {
	blocked := make(chan struct{})
	loop, _, _ := pauseLoop(t, func(ctx context.Context, in PauseIn) (string, bool) {
		close(blocked)
		return in.Block(ctx)
	})
	events := loop.Start(context.Background(), basicMessages())
	<-blocked
	require.True(t, loop.SendControlSignal(NewStopControl()))

	all := collectEvents(t, events, 5*time.Second)
	assert.Equal(t, DoneReasonStopped, doneOf(t, all).Reason)
}

func TestPause_ContextCancelledWhileBlocked(t *testing.T) {
	blocked := make(chan struct{})
	loop, _, _ := pauseLoop(t, func(ctx context.Context, in PauseIn) (string, bool) {
		close(blocked)
		return in.Block(ctx)
	})
	ctx, cancel := context.WithCancel(context.Background())
	events := loop.Start(ctx, basicMessages())
	<-blocked
	cancel()

	all := collectEvents(t, events, 5*time.Second)
	assert.Equal(t, DoneReasonStopped, doneOf(t, all).Reason)
	assert.Equal(t, StateStopped, loop.GetState())
}

func TestPause_ModifyConfigWhileBlocked(t *testing.T) {
	blocked := make(chan struct{})
	loop, llm, _ := pauseLoop(t, func(ctx context.Context, in PauseIn) (string, bool) {
		close(blocked)
		return in.Block(ctx)
	})
	events := loop.Start(context.Background(), basicMessages())
	<-blocked
	temp := 0.9
	require.True(t, loop.SendControlSignal(NewModifyConfigControl(ModifyConfigData{Temperature: &temp})))
	require.True(t, loop.Inject("ok"))

	collectEvents(t, events, 5*time.Second)
	require.NotNil(t, llm.LastOptions().Temperature)
	assert.InDelta(t, 0.9, *llm.LastOptions().Temperature, 0.001)
}

func TestPause_AwaitingHumanEndsRun(t *testing.T) {
	type gap struct{ ID string }
	loop, llm, _ := pauseLoop(t, func(_ context.Context, in PauseIn) (string, bool) {
		in.Emit(EventAwaitingInput, AwaitingInputData{
			Question: "Which colour?",
			Options:  []string{"red", "blue"},
			Payload:  gap{ID: "g-1"},
		})
		return "", false
	})
	all := collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)

	done := doneOf(t, all)
	assert.Equal(t, DoneReasonAwaitingHuman, done.Reason)
	assert.Equal(t, "Which colour?", done.FinalContent)
	require.NotNil(t, done.Awaiting)
	assert.Equal(t, []string{"red", "blue"}, done.Awaiting.Options)
	assert.Equal(t, gap{ID: "g-1"}, done.Awaiting.Payload)
	assert.Equal(t, StateDone, loop.GetState())
	assert.Equal(t, 1, llm.CallCount())
	assert.Nil(t, findEvent(all, EventToolResult), "no tool result is recorded for an unanswered pause")
}

func TestPause_AwaitingAcceptsPointerPayload(t *testing.T) {
	loop, _, _ := pauseLoop(t, func(_ context.Context, in PauseIn) (string, bool) {
		in.Emit(EventAwaitingInput, &AwaitingInputData{Question: "Q?"})
		return "", false
	})
	all := collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)
	assert.Equal(t, DoneReasonAwaitingHuman, doneOf(t, all).Reason)
}

func TestPause_EndWithoutAwaitingIsStopped(t *testing.T) {
	for name, reply := range map[string]string{"empty reply": "", "reply but no continue": "bye"} {
		t.Run(name, func(t *testing.T) {
			loop, _, _ := pauseLoop(t, func(context.Context, PauseIn) (string, bool) { return reply, false })
			all := collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)
			assert.Equal(t, DoneReasonStopped, doneOf(t, all).Reason)
		})
	}
}

func TestPause_ExecuteAndCall(t *testing.T) {
	lookup, err := NewToolInfo("lookup", "Look something up", nil)
	require.NoError(t, err)

	var toolOut, callOut string
	loop, llm, exec := pauseLoop(t, func(ctx context.Context, in PauseIn) (string, bool) {
		toolOut, _ = in.Execute(ctx, "lookup", `{"q":1}`)
		msg, cerr := in.Call(ctx, []*schema.ToolInfo{lookup}, in.Messages)
		if cerr == nil && msg != nil {
			callOut = msg.Content
		}
		return "done", true
	})
	collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)

	assert.Equal(t, "result from lookup", toolOut)
	assert.Equal(t, []string{"lookup"}, exec.CalledTools())
	assert.Equal(t, "thanks", callOut, "Call returns the next scripted reply")
	require.Len(t, llm.BoundTools(), 1)
	assert.Equal(t, "lookup", llm.BoundTools()[0][0].Name)
}

func TestPause_PanicBecomesToolError(t *testing.T) {
	loop, llm, _ := pauseLoop(t, func(context.Context, PauseIn) (string, bool) { panic("pause boom") })
	all := collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)

	assert.Equal(t, DoneReasonFinished, doneOf(t, all).Reason)
	last := llm.LastMessages()
	assert.Contains(t, last[len(last)-1].Content, "pause boom")
	errEvt := findEvent(all, EventError)
	require.NotNil(t, errEvt)
	assert.False(t, errEvt.Data.(ErrorData).Fatal)
}

func TestPause_OtherToolsStillReachExecutor(t *testing.T) {
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{ToolCalls: []schema.ToolCall{
			toolCall("c1", "get_profile", `{}`),
			toolCall("c2", "ask_user", `{}`),
			toolCall("c3", "search_content", `{"query":"x"}`),
		}},
		steerlooptest.Step{Content: "ok"},
	)
	exec := steerlooptest.NewRecordingExecutor(nil)
	cfg := basicConfig()
	cfg.Pauses = map[string]PauseFunc{"ask_user": func(context.Context, PauseIn) (string, bool) { return "yes", true }}
	loop, err := New(cfg, llm, sampleTools(), exec, nil)
	require.NoError(t, err)
	all := collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)

	assert.Equal(t, []string{"get_profile", "search_content"}, exec.CalledTools())
	assert.Len(t, findEvents(all, EventToolResult), 3)
}
