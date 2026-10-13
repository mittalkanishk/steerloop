package steerloop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// errEmptyResponse marks a reply with no text and no tool call, so the retry
// logic can recognise it with errors.Is.
var errEmptyResponse = errors.New("steerloop: empty LLM response")

// callModel makes one logical model call: the step timeout, streaming with an
// idle-chunk watchdog, and retries on transient failure. It does not change
// msgs. It returns a complete assistant message or an error.
func (l *Loop) callModel(totalCtx context.Context, m model.ToolCallingChatModel, msgs []*schema.Message) (*schema.Message, error) {
	attempts := 1 + l.effectiveRetryAttempts()

	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			// Back off briefly so a flaky provider is not hammered.
			backoff := time.Duration(500*attempt) * time.Millisecond
			select {
			case <-time.After(backoff):
			case <-totalCtx.Done():
				return nil, totalCtx.Err()
			}
			l.logger.Info("agent loop retrying transient LLM failure",
				slog.Int("attempt", attempt), slog.String("last_error", lastErr.Error()))
		}

		msg, err := l.singleCall(totalCtx, m, msgs)
		if err == nil && !isEmptyMessage(msg) {
			return msg, nil
		}
		if err == nil {
			// No error and nothing useful: treat as transient.
			err = errEmptyResponse
		}
		lastErr = err

		if totalCtx.Err() != nil || !l.isTransient(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// singleCall makes one attempt with the configured step timeout. It does not retry.
func (l *Loop) singleCall(totalCtx context.Context, m model.ToolCallingChatModel, msgs []*schema.Message) (*schema.Message, error) {
	stepCtx, stepCancel := l.buildStepCtx(totalCtx)
	defer stepCancel()

	temp := float32(l.temperature())
	var opts []model.Option
	if l.cfg.CallOptions != nil {
		opts = l.cfg.CallOptions(temp)
	} else {
		opts = []model.Option{model.WithTemperature(temp)}
	}

	if l.cfg.DisableStreaming {
		return m.Generate(stepCtx, msgs, opts...)
	}
	return l.streamAndCollect(stepCtx, m, msgs, opts)
}

// buildStepCtx returns the context for one model call. By default the call's
// deadline is the full StepTimeout, not the smaller of StepTimeout and what is
// left of TotalTimeout. The call is still cancelled when the run ends.
func (l *Loop) buildStepCtx(parent context.Context) (context.Context, context.CancelFunc) {
	if l.cfg.StepTimeout <= 0 {
		return context.WithCancel(parent)
	}
	if l.cfg.DisableStepTimeoutDecoupling {
		return context.WithTimeout(parent, l.cfg.StepTimeout)
	}

	detached := context.WithoutCancel(parent)
	stepCtx, stepCancel := context.WithTimeout(detached, l.cfg.StepTimeout)

	// Cancel the step when the run is cancelled or its total deadline passes.
	// Only the step's own deadline is independent of the parent's.
	go func() {
		select {
		case <-parent.Done():
			stepCancel()
		case <-stepCtx.Done():
		}
	}()
	return stepCtx, stepCancel
}

// streamAndCollect runs a streamed call and joins the chunks. If more than
// IdleChunkTimeout passes between chunks after the first one, the call is
// treated as hung and returns a transient error.
func (l *Loop) streamAndCollect(ctx context.Context, m model.ToolCallingChatModel, msgs []*schema.Message, opts []model.Option) (*schema.Message, error) {
	sr, err := m.Stream(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	defer sr.Close()

	idleTimeout := l.cfg.IdleChunkTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleChunkTimeout
	}

	type recvOut struct {
		m   *schema.Message
		err error
	}
	recvCh := make(chan recvOut)
	done := make(chan struct{})
	defer close(done)

	go func() {
		defer close(recvCh)
		for {
			chunk, rerr := sr.Recv()
			select {
			case recvCh <- recvOut{chunk, rerr}:
			case <-done:
				return
			}
			if rerr != nil {
				return
			}
		}
	}()

	var (
		chunks       []*schema.Message
		firstChunkOK bool
	)
	for {
		// Before the first chunk only the step timeout applies: a reply that
		// is only tool calls may send nothing until its final chunk.
		var idleCh <-chan time.Time
		if firstChunkOK {
			idleCh = time.After(idleTimeout)
		}

		select {
		case r, ok := <-recvCh:
			if !ok || errors.Is(r.err, io.EOF) {
				return schema.ConcatMessages(chunks)
			}
			if r.err != nil {
				return nil, r.err
			}
			firstChunkOK = true
			if r.m != nil {
				chunks = append(chunks, r.m)
			}

		case <-idleCh:
			return nil, fmt.Errorf("stream idle: no chunk received for %s", idleTimeout)

		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// effectiveRetryAttempts returns the number of extra attempts.
func (l *Loop) effectiveRetryAttempts() int {
	switch {
	case l.cfg.RetryMaxAttempts < 0:
		return 0
	case l.cfg.RetryMaxAttempts == 0:
		return 1
	default:
		return l.cfg.RetryMaxAttempts
	}
}

func (l *Loop) isTransient(err error) bool {
	if l.cfg.IsTransient != nil {
		return l.cfg.IsTransient(err)
	}
	return IsTransientLLMError(err)
}

func isEmptyMessage(msg *schema.Message) bool {
	if msg == nil {
		return true
	}
	return msg.Content == "" && len(msg.ToolCalls) == 0
}

// IsTransientLLMError is the default retry classifier. It reports true for a
// step deadline, EOF, an empty reply, an idle stream and a reset connection.
func IsTransientLLMError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, errEmptyResponse) {
		return true
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "stream idle"),
		strings.Contains(msg, "empty response"),
		strings.Contains(msg, "no response"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "unexpected eof"):
		return true
	}
	return false
}
