package loop2

import (
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/event"
	"github.com/mittalkanishk/steerloop/loop2/pause"
)

// FinishToolName is the reserved tool that ends a StopOnFinishTool run. Its
// arguments become DoneData.FinalContent. It never reaches the ToolExecutor.
const FinishToolName = "finish"

// StopCondition decides when the loop ends on its own.
type StopCondition int

const (
	// StopOnNoToolCalls ends the loop when the model replies without calling
	// a tool. Suits chat and question answering.
	StopOnNoToolCalls StopCondition = iota
	// StopOnFinishTool runs until the model calls FinishToolName.
	StopOnFinishTool
	// StopOnExplicit runs until a stop signal, the iteration cap or a timeout.
	StopOnExplicit
)

// The interfaces below are what a caller may plug into Config. Every one is
// optional: wire.go uses a built-in default when a field is nil.

// EventSink receives every event, synchronously, before it is offered to the
// event channel. It must not block for long.
type EventSink interface {
	Write(evt event.Event)
}

// HistoryCompactor shapes the history before each model call. Its result is
// what the model receives; the stored history is not changed.
type HistoryCompactor interface {
	Compact(msgs []*schema.Message) []*schema.Message
}

// CallOptionsBuilder builds the per-call model options. Default: temperature only.
type CallOptionsBuilder interface {
	Build(temperature float32) []model.Option
}

// ModelIdentifier names the provider and model for LLMTurn events. It is
// asked once per turn, so it may change during a run.
type ModelIdentifier interface {
	Identity() (provider, model string)
}

// CostEstimator prices one turn for LLMTurn events. Default: 0.
type CostEstimator interface {
	Estimate(provider, model string, inputTokens, outputTokens int) float64
}

// ErrorClassifier decides whether a failed model call is worth retrying.
// Default: deadline exceeded, EOF, empty reply, idle stream, connection reset.
type ErrorClassifier interface {
	IsTransient(err error) bool
}

// Config configures one Loop. It holds values and interfaces only, no
// function fields. DefaultConfig returns sensible values.
type Config struct {
	MaxIterations int           // cap on model calls; the loop never exceeds it
	StopCondition StopCondition // when the loop ends on its own
	Temperature   float64
	StepTimeout   time.Duration // bound on one model call; zero: none
	TotalTimeout  time.Duration // bound on the whole run; zero: none

	EnableReflection bool   // one self-review call after the final reply (StopOnNoToolCalls)
	ReflectionPrompt string // empty: a built-in neutral prompt

	StreamEvents      bool          // false: only error and done events
	EventBuffer       int           // event channel capacity; zero: 64
	EventSink         EventSink     // optional
	DoneDeliveryGrace time.Duration // wait for a reader of the done event; zero: 5s

	Pauses map[string]pause.Handler // tool name → handler, in place of the ToolExecutor

	AutoNudgeText    string // sent after a text-only reply in StopOnFinishTool; empty: built-in
	DisableAutoNudge bool

	RetryMaxAttempts int // extra attempts after a transient failure; zero: 1; -1: none
	ErrorClassifier  ErrorClassifier

	DisableStreaming             bool          // call Generate instead of Stream
	IdleChunkTimeout             time.Duration // longest gap between streamed chunks; zero: 30s
	DisableStepTimeoutDecoupling bool          // each call inherits the run's remaining time

	HistoryCompactor HistoryCompactor
	CallOptions      CallOptionsBuilder
	ModelIdentity    ModelIdentifier
	CostEstimator    CostEstimator

	TranscriptLogLevel slog.Level // level for transcript lines (they contain prompt text)
}

// DefaultConfig suits an interactive assistant: up to 12 model calls, stop
// when the model answers without a tool call, intermediate events on.
func DefaultConfig() Config {
	return Config{
		MaxIterations:      12,
		StopCondition:      StopOnNoToolCalls,
		Temperature:        0.2,
		StepTimeout:        60 * time.Second,
		TotalTimeout:       10 * time.Minute,
		StreamEvents:       true,
		TranscriptLogLevel: slog.LevelDebug,
	}
}

const (
	defaultEventBuffer       = 64
	defaultDoneDeliveryGrace = 5 * time.Second
	defaultIdleChunkTimeout  = 30 * time.Second

	// defaultAutoNudge unblocks a model that narrated instead of acting,
	// without leaving a trailing assistant message some providers reject.
	defaultAutoNudge = `Your previous turn returned prose instead of a tool call. If you have more work to do, call the appropriate tool now. When the job is complete, call the finish() tool with the final structured result.`

	defaultReflectionPrompt = "Review your previous response critically. Is it complete, accurate, " +
		"and well-structured? Suggest improvements if needed."
)
