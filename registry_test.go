package steerloop

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

	"github.com/mittalkanishk/steerloop/steerlooptest"
)

func TestRegistry_Lifecycle(t *testing.T) {
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
	loop, err := New(cfg, llm, nil, steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)

	reg := NewRegistry[string]()
	assert.False(t, reg.Has("session-1"))
	assert.ErrorIs(t, reg.Stop("session-1"), ErrNoActiveLoop)
	assert.ErrorIs(t, reg.Inject("session-1", "hi"), ErrNoActiveLoop)
	_, err = reg.RunningMessages("session-1")
	assert.ErrorIs(t, err, ErrNoActiveLoop)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := loop.Start(ctx, basicMessages())
	for llm.CallCount() == 0 {
		time.Sleep(time.Millisecond) // the loop is now held inside its first model call
	}
	reg.Register("session-1", loop, cancel)
	reg.Register("ignored", nil, nil)
	assert.True(t, reg.Has("session-1"))
	assert.False(t, reg.Has("ignored"))

	al, ok := reg.Get("session-1")
	require.True(t, ok)
	assert.Same(t, loop, al.Loop)

	require.NoError(t, reg.Inject("session-1", "steer left"))
	msgs, err := reg.RunningMessages("session-1")
	require.NoError(t, err)
	assert.Len(t, msgs, 2)

	for i := 1; i < controlChBuffer; i++ {
		require.NoError(t, reg.Inject("session-1", "more"))
	}
	assert.ErrorIs(t, reg.Inject("session-1", "overflow"), ErrControlQueueFull)

	require.NoError(t, reg.Stop("session-1"))
	close(gate)
	all := collectEvents(t, events, 5*time.Second)
	assert.Equal(t, DoneReasonStopped, doneOf(t, all).Reason)

	reg.Unregister("session-1")
	assert.False(t, reg.Has("session-1"))
}

func TestRegistry_NilAndStructKeys(t *testing.T) {
	var nilReg *Registry[int]
	nilReg.Register(1, nil, nil)
	nilReg.Unregister(1)
	assert.False(t, nilReg.Has(1))
	assert.ErrorIs(t, nilReg.Stop(1), ErrNoActiveLoop)

	type key struct{ Tenant, Run string }
	reg := NewRegistry[key]()
	loop, err := New(basicConfig(), steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "x"}), nil, steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)
	reg.Register(key{"a", "1"}, loop, nil)
	assert.True(t, reg.Has(key{"a", "1"}))
	assert.False(t, reg.Has(key{"a", "2"}))
}

func TestRegistry_ConcurrentUse(t *testing.T) {
	reg := NewRegistry[string]()
	loop, err := New(basicConfig(), steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "x"}), nil, steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i%4)
			for j := 0; j < 200; j++ {
				reg.Register(key, loop, nil)
				reg.Has(key)
				_, _ = reg.RunningMessages(key)
				reg.Unregister(key)
			}
		}(i)
	}
	wg.Wait()
}
