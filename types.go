package steerloop

import (
	"context"
	"log/slog"
)

// LoopState is the lifecycle state of a Loop.
type LoopState string

const (
	// StateIdle is the state before Start.
	StateIdle LoopState = "idle"
	// StateRunning is the state between Start and the done event.
	StateRunning LoopState = "running"
	// StateDone means the loop ended on its own: finished, max iterations,
	// timeout, or handed over to a human.
	StateDone LoopState = "done"
	// StateStopped means the loop ended because it was told to stop.
	StateStopped LoopState = "stopped"
	// StateError means the loop ended on a fatal error.
	StateError LoopState = "error"
)

const (
	defaultEventBuffer = 64
	controlChBuffer    = 8
)

type llmUsageAccumulator struct {
	input     int
	output    int
	totalOnly int
	model     string
}

func (acc llmUsageAccumulator) summary() LLMUsageSummary {
	return LLMUsageSummary{
		InputTokens:  acc.input,
		OutputTokens: acc.output,
		TotalTokens:  acc.input + acc.output + acc.totalOnly,
		Model:        acc.model,
	}
}

// discardHandler is a slog.Handler that drops every record.
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (discardHandler) WithAttrs([]slog.Attr) slog.Handler        { return discardHandler{} }
func (discardHandler) WithGroup(string) slog.Handler             { return discardHandler{} }
