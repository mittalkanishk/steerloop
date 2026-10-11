package steerloop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Kanishkmittal55/steerloop/steerlooptest"
)

func runSteps(t *testing.T, cfg LoopConfig, steps ...steerlooptest.Step) ([]Event, *steerlooptest.ScriptedModel) {
	t.Helper()
	llm := steerlooptest.NewScriptedModel(steps...)
	loop, err := New(cfg, llm, nil, steerlooptest.NewRecordingExecutor(nil), nil)
	require.NoError(t, err)
	return collectEvents(t, loop.Start(context.Background(), basicMessages()), 15*time.Second), llm
}

func TestRetry_TransientThenSuccess(t *testing.T) {
	all, llm := runSteps(t, basicConfig(),
		steerlooptest.Step{Err: io.ErrUnexpectedEOF},
		steerlooptest.Step{Content: "recovered"},
	)
	done := doneOf(t, all)
	assert.Equal(t, DoneReasonFinished, done.Reason)
	assert.Equal(t, "recovered", done.FinalContent)
	assert.Equal(t, 2, llm.CallCount())
	assert.Equal(t, 1, done.Iterations, "a retry is not a new iteration")
}

func TestRetry_TransientTwiceIsFatalByDefault(t *testing.T) {
	all, llm := runSteps(t, basicConfig(),
		steerlooptest.Step{Err: errors.New("read: connection reset by peer")},
	)
	done := doneOf(t, all)
	assert.Equal(t, DoneReasonError, done.Reason)
	assert.Contains(t, done.Error, "connection reset")
	assert.Equal(t, 2, llm.CallCount(), "one attempt plus the default single retry")
}

func TestRetry_FatalErrorIsNotRetried(t *testing.T) {
	all, llm := runSteps(t, basicConfig(), steerlooptest.Step{Err: errors.New("invalid api key")})
	assert.Equal(t, DoneReasonError, doneOf(t, all).Reason)
	assert.Equal(t, 1, llm.CallCount())
}

func TestRetry_Disabled(t *testing.T) {
	cfg := basicConfig()
	cfg.RetryMaxAttempts = -1
	all, llm := runSteps(t, cfg, steerlooptest.Step{Err: io.EOF}, steerlooptest.Step{Content: "never"})
	assert.Equal(t, DoneReasonError, doneOf(t, all).Reason)
	assert.Equal(t, 1, llm.CallCount())
}

func TestRetry_ConfiguredAttempts(t *testing.T) {
	cfg := basicConfig()
	cfg.RetryMaxAttempts = 2
	all, llm := runSteps(t, cfg,
		steerlooptest.Step{Err: io.EOF},
		steerlooptest.Step{Err: io.EOF},
		steerlooptest.Step{Content: "third time"},
	)
	assert.Equal(t, "third time", doneOf(t, all).FinalContent)
	assert.Equal(t, 3, llm.CallCount())
}

func TestRetry_EmptyReplyIsTransient(t *testing.T) {
	all, llm := runSteps(t, basicConfig(), steerlooptest.Step{}, steerlooptest.Step{Content: "now with text"})
	assert.Equal(t, "now with text", doneOf(t, all).FinalContent)
	assert.Equal(t, 2, llm.CallCount())
}

func TestRetry_CustomClassifier(t *testing.T) {
	cfg := basicConfig()
	cfg.IsTransient = func(err error) bool { return err != nil && err.Error() == "rate limited" }
	all, llm := runSteps(t, cfg,
		steerlooptest.Step{Err: errors.New("rate limited")},
		steerlooptest.Step{Content: "ok"},
	)
	assert.Equal(t, "ok", doneOf(t, all).FinalContent)
	assert.Equal(t, 2, llm.CallCount())

	all, llm = runSteps(t, cfg, steerlooptest.Step{Err: io.EOF})
	assert.Equal(t, DoneReasonError, doneOf(t, all).Reason)
	assert.Equal(t, 1, llm.CallCount(), "the custom classifier replaces the default")
}

func TestRetry_StepTimeoutIsRetried(t *testing.T) {
	cfg := basicConfig()
	cfg.StepTimeout = 40 * time.Millisecond
	all, llm := runSteps(t, cfg,
		steerlooptest.Step{Content: "too slow", Delay: 500 * time.Millisecond},
		steerlooptest.Step{Content: "fast"},
	)
	assert.Equal(t, "fast", doneOf(t, all).FinalContent)
	assert.Equal(t, 2, llm.CallCount())
}

func TestRetry_StopsWhenRunContextEnds(t *testing.T) {
	cfg := basicConfig()
	cfg.TotalTimeout = 100 * time.Millisecond
	cfg.RetryMaxAttempts = 5
	start := time.Now()
	all, _ := runSteps(t, cfg, steerlooptest.Step{Err: io.EOF})
	assert.Equal(t, DoneReasonError, doneOf(t, all).Reason)
	assert.Less(t, time.Since(start), 2*time.Second, "backoff must not outlive the run")
}

func TestIsTransientLLMError(t *testing.T) {
	transient := []error{
		context.DeadlineExceeded, io.EOF, io.ErrUnexpectedEOF, errEmptyResponse,
		fmt.Errorf("wrapped: %w", io.EOF),
		errors.New("stream idle: no chunk received for 30s"),
		errors.New("provider returned Empty Response"),
		errors.New("no response from upstream"),
		errors.New("Connection Reset by peer"),
	}
	for _, err := range transient {
		assert.True(t, IsTransientLLMError(err), err.Error())
	}
	for _, err := range []error{nil, context.Canceled, errors.New("401 unauthorized")} {
		assert.False(t, IsTransientLLMError(err))
	}
}

func streamingConfig() LoopConfig {
	cfg := basicConfig()
	cfg.DisableStreaming = false
	return cfg
}

func TestStream_ChunksAreJoined(t *testing.T) {
	all, _ := runSteps(t, streamingConfig(), steerlooptest.Step{
		Chunks: []steerlooptest.Chunk{{Content: "Hel"}, {Content: "lo "}, {Content: "there"}},
	})
	assert.Equal(t, "Hello there", doneOf(t, all).FinalContent)
}

func TestStream_ToolCallsOnStreamingPath(t *testing.T) {
	llm := steerlooptest.NewScriptedModel(
		steerlooptest.Step{
			Chunks:    []steerlooptest.Chunk{{Content: ""}, {Content: ""}},
			ToolCalls: []schema.ToolCall{toolCall("c1", "get_profile", `{}`)},
		},
		steerlooptest.Step{Content: "used the tool"},
	)
	exec := steerlooptest.NewRecordingExecutor(nil)
	loop, err := New(streamingConfig(), llm, sampleTools(), exec, nil)
	require.NoError(t, err)
	all := collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)
	assert.Equal(t, "used the tool", doneOf(t, all).FinalContent)
	assert.Equal(t, []string{"get_profile"}, exec.CalledTools())
}

func TestStream_IdleWatchdogFiresAfterFirstChunk(t *testing.T) {
	cfg := streamingConfig()
	cfg.IdleChunkTimeout = 40 * time.Millisecond
	cfg.RetryMaxAttempts = -1
	all, _ := runSteps(t, cfg, steerlooptest.Step{
		Chunks: []steerlooptest.Chunk{{Content: "start"}, {Content: "never", Delay: 2 * time.Second}},
	})
	done := doneOf(t, all)
	assert.Equal(t, DoneReasonError, done.Reason)
	assert.Contains(t, done.Error, "stream idle")
}

func TestStream_IdleWatchdogIgnoresSlowFirstChunk(t *testing.T) {
	cfg := streamingConfig()
	cfg.IdleChunkTimeout = 40 * time.Millisecond
	all, llm := runSteps(t, cfg, steerlooptest.Step{
		Chunks: []steerlooptest.Chunk{{Content: "late but fine", Delay: 200 * time.Millisecond}},
	})
	assert.Equal(t, "late but fine", doneOf(t, all).FinalContent)
	assert.Equal(t, 1, llm.CallCount())
}

func TestStream_IdleStreamIsRetried(t *testing.T) {
	cfg := streamingConfig()
	cfg.IdleChunkTimeout = 40 * time.Millisecond
	all, llm := runSteps(t, cfg,
		steerlooptest.Step{Chunks: []steerlooptest.Chunk{{Content: "a"}, {Content: "b", Delay: 2 * time.Second}}},
		steerlooptest.Step{Content: "second attempt"},
	)
	assert.Equal(t, "second attempt", doneOf(t, all).FinalContent)
	assert.Equal(t, 2, llm.CallCount())
}

func TestStream_ErrorFromStreamCall(t *testing.T) {
	cfg := streamingConfig()
	cfg.RetryMaxAttempts = -1
	all, _ := runSteps(t, cfg, steerlooptest.Step{Err: errors.New("bad request")})
	assert.Contains(t, doneOf(t, all).Error, "bad request")
}

func TestStream_GenerateUsedWhenStreamingDisabled(t *testing.T) {
	var generate, stream int
	m := &countingModel{inner: steerlooptest.NewScriptedModel(steerlooptest.Step{Content: "hi"}), generate: &generate, stream: &stream}
	for _, disable := range []bool{true, false} {
		cfg := basicConfig()
		cfg.DisableStreaming = disable
		loop, err := New(cfg, m, nil, steerlooptest.NewRecordingExecutor(nil), nil)
		require.NoError(t, err)
		collectEvents(t, loop.Start(context.Background(), basicMessages()), 5*time.Second)
	}
	assert.Equal(t, 1, generate)
	assert.Equal(t, 1, stream)
}

type countingModel struct {
	inner            *steerlooptest.ScriptedModel
	generate, stream *int
}

func (c *countingModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	*c.generate++
	return c.inner.Generate(ctx, in, opts...)
}

func (c *countingModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	*c.stream++
	return c.inner.Stream(ctx, in, opts...)
}

func (c *countingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return c, nil
}

func TestStepTimeout_Decoupling(t *testing.T) {
	// The run's total deadline passes while a model call is in flight. With
	// decoupling the call is cancelled; without it the call's own deadline is
	// the remaining total time, so it fails with a deadline error.
	for _, tc := range []struct {
		disable bool
		want    string
	}{{false, "context canceled"}, {true, "deadline exceeded"}} {
		cfg := basicConfig()
		cfg.TotalTimeout = 60 * time.Millisecond
		cfg.StepTimeout = 5 * time.Second
		cfg.DisableStepTimeoutDecoupling = tc.disable
		start := time.Now()
		all, _ := runSteps(t, cfg, steerlooptest.Step{Content: "slow", Delay: 3 * time.Second})
		done := doneOf(t, all)
		assert.Equal(t, DoneReasonError, done.Reason)
		assert.Contains(t, done.Error, tc.want)
		assert.Less(t, time.Since(start), 2*time.Second)
	}
}

func TestStepTimeout_ZeroMeansNoStepBound(t *testing.T) {
	cfg := basicConfig()
	cfg.StepTimeout = 0
	all, _ := runSteps(t, cfg, steerlooptest.Step{Content: "ok", Delay: 50 * time.Millisecond})
	assert.Equal(t, "ok", doneOf(t, all).FinalContent)
}
