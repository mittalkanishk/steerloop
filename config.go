package steerloop

import (
	"log/slog"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// StopCondition decides when the loop ends on its own.
type StopCondition int

const (
	// StopOnNoToolCalls ends the loop when the model replies without calling
	// a tool. Suits chat and question answering.
	StopOnNoToolCalls StopCondition = iota

	// StopOnFinishTool runs until the model calls the tool named
	// FinishToolName; its arguments are the final output. Suits work that must
	// end in a structured result.
	StopOnFinishTool

	// StopOnExplicit runs until a stop signal, the iteration cap or a timeout.
	StopOnExplicit
)

// StopConditionLabel returns the stable name used in llm_turn events.
func StopConditionLabel(sc StopCondition) string {
	switch sc {
	case StopOnNoToolCalls:
		return "no_tool_calls"
	case StopOnFinishTool:
		return "finish_tool"
	case StopOnExplicit:
		return "explicit"
	default:
		return ""
	}
}

// LoopConfig configures one Loop. The zero value is usable only after
// MaxIterations is set; DefaultConfig returns sensible values.
type LoopConfig struct {
	// MaxIterations caps the number of model calls. The loop never exceeds it.
	MaxIterations int
	// StopCondition decides when the loop ends on its own.
	StopCondition StopCondition
	// Temperature is passed to the model on every call.
	Temperature float64
	// StepTimeout bounds one model call. Zero means no per-call bound.
	StepTimeout time.Duration
	// TotalTimeout bounds the whole run. Zero means no bound.
	TotalTimeout time.Duration

	// EnableReflection adds one self-review call after the final reply in
	// StopOnNoToolCalls mode.
	EnableReflection bool
	// ReflectionPrompt is the instruction for that call. The loop appends the
	// reply under review. Empty means a built-in neutral prompt.
	ReflectionPrompt string

	// StreamEvents turns on intermediate events. When false only error and
	// done events are emitted.
	StreamEvents bool
	// EventBuffer is the capacity of the event channel. Zero means 64.
	// Intermediate events are dropped, and counted, when it is full.
	EventBuffer int
	// EventSink, when set, receives every emitted event.
	EventSink EventSink
	// DoneDeliveryGrace is how long the loop waits for a reader to take the
	// done event when the event channel is full. Zero means 5 seconds.
	DoneDeliveryGrace time.Duration

	// Pauses maps a tool name to a pause function. A call to such a tool is
	// handed to the function instead of the ToolExecutor.
	Pauses map[string]PauseFunc

	// AutoNudgeText is the user message appended when the model replies with
	// text only in StopOnFinishTool mode. Empty means a built-in nudge.
	AutoNudgeText string
	// DisableAutoNudge turns that message off.
	DisableAutoNudge bool

	// RetryMaxAttempts is the number of extra attempts after a transient model
	// failure. Zero means 1; -1 disables retries.
	RetryMaxAttempts int
	// IsTransient classifies a model error as worth retrying. Nil means the
	// built-in classifier (deadline exceeded, EOF, empty reply, idle stream,
	// connection reset).
	IsTransient func(error) bool

	// DisableStreaming makes the loop call Generate instead of Stream.
	// Streaming is what lets the loop detect a hung turn early.
	DisableStreaming bool
	// IdleChunkTimeout is the longest gap allowed between streamed chunks
	// after the first one. Zero means 30 seconds.
	IdleChunkTimeout time.Duration
	// DisableStepTimeoutDecoupling makes each model call inherit the remaining
	// TotalTimeout. By default a call gets the full StepTimeout even when less
	// of TotalTimeout remains; cancellation of the run still reaches it.
	DisableStepTimeoutDecoupling bool

	// HistoryCompactor, when set, is applied to the message history before
	// each model call. Its result is what the model receives; the stored
	// history is not changed.
	HistoryCompactor func(msgs []*schema.Message) []*schema.Message

	// CallOptions builds the per-call model options. Nil means
	// model.WithTemperature(temperature).
	CallOptions func(temperature float32) []model.Option
	// ModelIdentity names the provider and model for llm_turn events. It is
	// called once per turn, so it may change during a run.
	ModelIdentity func() (provider, model string)
	// CostEstimator prices one turn for llm_turn events. Nil means cost 0.
	CostEstimator func(provider, model string, inputTokens, outputTokens int) float64

	// TranscriptLogLevel is the level at which the loop logs the messages
	// added on each iteration. These lines contain prompt and reply text. The
	// zero value is slog.LevelInfo; DefaultConfig sets slog.LevelDebug to keep
	// them out of normal logs.
	TranscriptLogLevel slog.Level
}

// DefaultConfig returns a configuration suited to an interactive assistant:
// up to 12 model calls, stop when the model answers without a tool call, and
// intermediate events on.
func DefaultConfig() LoopConfig {
	return LoopConfig{
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
	// defaultAutoNudge unblocks a model that narrated instead of acting, without
	// leaving a trailing assistant message that some providers reject.
	defaultAutoNudge = `Your previous turn returned prose instead of a tool call. If you have more work to do, call the appropriate tool now. When the job is complete, call the finish() tool with the final structured result.`

	defaultReflectionPrompt = "Review your previous response critically. Is it complete, accurate, " +
		"and well-structured? Suggest improvements if needed."

	defaultIdleChunkTimeout  = 30 * time.Second
	defaultDoneDeliveryGrace = 5 * time.Second
)

const (
	defaultEventBuffer = 64
	controlChBuffer    = 8
)
