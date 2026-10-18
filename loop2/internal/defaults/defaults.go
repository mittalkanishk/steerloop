// Package defaults is what runs when the caller supplies nothing: one no-op
// or built-in implementation per optional interface, so no other package has
// to check for nil.
package defaults

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/loop2/internal/modelcall"
)

// NoSink discards events. Implements emitter.Sink.
type NoSink struct{}

func (NoSink) Write(event.Event) {}

// FullHistory sends the whole history to the model. Implements
// engine.HistoryCompactor.
type FullHistory struct{}

func (FullHistory) Compact(msgs []*schema.Message) []*schema.Message { return msgs }

// TemperatureOnly passes just the temperature. Implements
// modelcall.OptionsBuilder.
type TemperatureOnly struct{}

func (TemperatureOnly) Build(temperature float32) []model.Option {
	return []model.Option{model.WithTemperature(temperature)}
}

// NoIdentity reports no provider or model. Implements audit.ModelIdentifier.
type NoIdentity struct{}

func (NoIdentity) Identity() (provider, model string) { return "", "" }

// ZeroCost prices every turn at 0. Implements audit.CostEstimator.
type ZeroCost struct{}

func (ZeroCost) Estimate(string, string, int, int) float64 { return 0 }

// Classifier is the built-in retry classifier. Implements
// modelcall.ErrorClassifier.
type Classifier struct{}

func (Classifier) IsTransient(err error) bool { return IsTransientLLMError(err) }

// IsTransientLLMError reports true for a step deadline, EOF, an empty reply,
// an idle stream and a reset connection.
func IsTransientLLMError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, modelcall.ErrEmptyResponse) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"stream idle", "empty response", "no response", "connection reset", "unexpected eof"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// DiscardLogger drops every record.
func DiscardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
