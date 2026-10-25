package registry_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/mittalkanishk/steerloop/loop2"
	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/loop2/registry"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const eventTimeout = 15 * time.Second

// newLoop builds a loop that runs until it is stopped, against llm.
func newLoop(t *testing.T, llm model.ToolCallingChatModel) *loop2.Loop {
	t.Helper()
	cfg := loop2.Config{
		MaxIterations:    10,
		StopCondition:    loop2.StopOnExternal,
		IterationTimeout: 5 * time.Second,
		TotalTimeout:     10 * time.Second,
		StreamEvents:     true,
		DisableStreaming: true,
	}
	loop, err := loop2.New(cfg, llm, nil, steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)
	return loop
}

// heldModel answers every call with text but holds its first call open until
// release is called, so the loop is known to be running.
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

// doneOf reads events until the channel closes and returns the done payload.
func doneOf(t *testing.T, events <-chan event.Event) event.DoneData {
	t.Helper()
	timeout := time.After(eventTimeout)
	var done *event.DoneData
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				require.NotNil(t, done, "the run ended without a done event")
				return *done
			}
			if d, isDone := evt.Data.(event.DoneData); isDone {
				done = &d
			}
		case <-timeout:
			require.FailNow(t, "the run did not end")
		}
	}
}

func TestRegistry_StopsAndSteersALoopByKey(t *testing.T) {
	llm, release := heldModel("tick")
	defer release()
	loop := newLoop(t, llm)
	reg := registry.New[string]()

	assert.False(t, reg.Has("session-1"))
	assert.ErrorIs(t, reg.Stop("session-1"), registry.ErrNoActiveLoop)
	assert.ErrorIs(t, reg.Inject("session-1", "hi"), registry.ErrNoActiveLoop)
	_, err := reg.Messages("session-1")
	assert.ErrorIs(t, err, registry.ErrNoActiveLoop)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := loop.Start(ctx, []*schema.Message{
		{Role: schema.System, Content: "You are a helpful assistant."},
		{Role: schema.User, Content: "Hello"},
	})
	require.Eventually(t, func() bool { return llm.CallCount() > 0 }, eventTimeout, time.Millisecond)
	reg.Register("session-1", loop, cancel)
	reg.Register("ignored", nil, nil)
	assert.True(t, reg.Has("session-1"))
	assert.False(t, reg.Has("ignored"), "a nil loop is not registered")

	active, ok := reg.Get("session-1")
	require.True(t, ok)
	assert.Same(t, loop, active.Loop)

	require.NoError(t, reg.Inject("session-1", "steer left"))
	msgs, err := reg.Messages("session-1")
	require.NoError(t, err)
	assert.Len(t, msgs, 3, "the seed, the stop instructions and the user message; the inject waits for the next boundary")

	// The control queue is bounded: it fills while the loop is held inside
	// its model call, and the next inject is refused.
	full := false
	for range 64 {
		if err := reg.Inject("session-1", "more"); err != nil {
			assert.ErrorIs(t, err, registry.ErrControlQueueFull)
			full = true
			break
		}
	}
	assert.True(t, full, "the control queue never filled")

	require.NoError(t, reg.Stop("session-1"))
	release()
	assert.Equal(t, event.ReasonStopped, doneOf(t, events).Reason)

	reg.Unregister("session-1")
	assert.False(t, reg.Has("session-1"))
}

func TestRegistry_AcceptsStructKeys(t *testing.T) {
	type key struct{ Tenant, Run string }
	reg := registry.New[key]()
	reg.Register(key{"a", "1"}, newLoop(t, steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "x"})), nil)
	assert.True(t, reg.Has(key{"a", "1"}))
	assert.False(t, reg.Has(key{"a", "2"}))
}

func TestRegistry_IsSafeForConcurrentUse(t *testing.T) {
	reg := registry.New[string]()
	loop := newLoop(t, steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "x"}))

	var workers sync.WaitGroup
	for i := range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			key := fmt.Sprintf("k%d", i%4)
			for range 200 {
				reg.Register(key, loop, nil)
				reg.Has(key)
				_, _ = reg.Messages(key)
				reg.Unregister(key)
			}
		}()
	}
	workers.Wait()
}
