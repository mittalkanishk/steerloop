// Package modelcall makes one logical model call: step timeout, streaming
// with an idle-chunk watchdog, and retries on transient failure.
package modelcall

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// TemperatureSource gives the temperature for the next call. → state.Run
type TemperatureSource interface {
	Temperature() float64
}

// OptionsBuilder builds the per-call model options.
// → the caller's loop2.CallOptionsBuilder, or defaults.TemperatureOnly
type OptionsBuilder interface {
	Build(temperature float32) []model.Option
}

// ErrorClassifier decides whether a failed call is worth retrying.
// → the caller's loop2.ErrorClassifier, or defaults.Classifier
type ErrorClassifier interface {
	IsTransient(err error) bool
}

// ErrEmptyResponse marks a reply with no text and no tool call, so a
// classifier can recognise it with errors.Is.
var ErrEmptyResponse = errors.New("steerloop: empty LLM response")

// Timing is how long calls may take and how often they are retried.
type Timing struct {
	StepTimeout         time.Duration // zero: no per-call bound
	DecoupleStepTimeout bool          // a call gets the full StepTimeout even near the run's deadline
	IdleChunkTimeout    time.Duration
	RetryAttempts       int // extra attempts after a transient failure
	Streaming           bool
}

type Caller struct {
	timing      Timing
	temperature TemperatureSource
	options     OptionsBuilder
	classifier  ErrorClassifier
	logger      *slog.Logger
}

func New(timing Timing, temperature TemperatureSource, options OptionsBuilder, classifier ErrorClassifier, logger *slog.Logger) *Caller {
	return &Caller{timing: timing, temperature: temperature, options: options, classifier: classifier, logger: logger}
}

// Call returns a complete assistant message or an error. It retries
// transient failures with a short backoff. It does not change msgs.
func (c *Caller) Call(runCtx context.Context, m model.ToolCallingChatModel, msgs []*schema.Message) (*schema.Message, error) {
	var lastErr error
	for attempt := range 1 + c.timing.RetryAttempts {
		if attempt > 0 {
			// Back off briefly so a flaky provider is not hammered.
			select {
			case <-time.After(time.Duration(500*attempt) * time.Millisecond):
			case <-runCtx.Done():
				return nil, runCtx.Err()
			}
			c.logger.Info("agent loop retrying transient LLM failure",
				slog.Int("attempt", attempt), slog.String("last_error", lastErr.Error()))
		}

		msg, err := c.attempt(runCtx, m, msgs)
		if err == nil && !isEmpty(msg) {
			return msg, nil
		}
		if err == nil {
			err = ErrEmptyResponse // no error and nothing useful: treat as transient
		}
		lastErr = err

		if runCtx.Err() != nil || !c.classifier.IsTransient(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// attempt makes one call under the step timeout. It does not retry.
func (c *Caller) attempt(runCtx context.Context, m model.ToolCallingChatModel, msgs []*schema.Message) (*schema.Message, error) {
	stepCtx, cancel := c.stepContext(runCtx)
	defer cancel()

	opts := c.options.Build(float32(c.temperature.Temperature()))
	if !c.timing.Streaming {
		return m.Generate(stepCtx, msgs, opts...)
	}
	return collectStream(stepCtx, m, msgs, opts, c.timing.IdleChunkTimeout)
}

// stepContext returns the context for one call. When decoupled, the call's
// deadline is the full StepTimeout rather than whatever is left of the run;
// cancelling the run still cancels the call.
//
// Goroutine: when decoupled, one watcher per call forwards the run's
// cancellation. It ends when the run ends or the step does (cancel is
// deferred by attempt).
func (c *Caller) stepContext(runCtx context.Context) (context.Context, context.CancelFunc) {
	if c.timing.StepTimeout <= 0 {
		return context.WithCancel(runCtx)
	}
	if !c.timing.DecoupleStepTimeout {
		return context.WithTimeout(runCtx, c.timing.StepTimeout)
	}

	stepCtx, cancel := context.WithTimeout(context.WithoutCancel(runCtx), c.timing.StepTimeout)
	go func() {
		select {
		case <-runCtx.Done():
			cancel()
		case <-stepCtx.Done():
		}
	}()
	return stepCtx, cancel
}

func isEmpty(msg *schema.Message) bool {
	return msg == nil || (msg.Content == "" && len(msg.ToolCalls) == 0)
}
