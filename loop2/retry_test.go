package loop2_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2"
	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// rateLimitedOnly is an ErrorClassifier that retries only "rate limited".
type rateLimitedOnly struct{}

func (rateLimitedOnly) IsTransient(err error) bool {
	return err != nil && err.Error() == "rate limited"
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
	llm := steerlooptest.NewScriptedModel(fails(errors.New("invalid api key")))
	loop := s.newLoop(llm)
	events := s.run(loop)

	failure := payload[event.ErrorData](s, events)
	s.True(failure.Fatal)
	s.Contains(failure.Error, "invalid api key")
	s.Equal(event.ReasonError, s.done(events).Reason)
	s.Equal(loop2.StateError, loop.State())
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
	events, llm := s.runScript(steerlooptest.Step{}, says("now with text"))

	s.Equal("now with text", s.done(events).FinalContent)
	s.Equal(2, llm.CallCount())
}

func (s *LoopSuite) TestRetry_APersistentlyEmptyReplyIsFatal() {
	events, llm := s.runScript(steerlooptest.Step{}) // the empty step repeats

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

	s.Equal(event.ReasonError, s.done(events).Reason)
	s.Less(time.Since(start), 2*time.Second, "the backoff must not outlive the run")
}

// The default classifier is internal to loop2, so it is checked through the
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
	s.Equal(event.ReasonError, done.Reason)
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
