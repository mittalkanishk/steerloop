package steerloop

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mittalkanishk/steerloop/steerlooptest"
)

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
