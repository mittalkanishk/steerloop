package steerloop

import "time"

// EventType classifies an event.
type EventType string

const (
	// EventThinking is emitted before each model call.
	EventThinking EventType = "thinking"
	// EventToolCall is emitted when the model asks for a tool, before it runs.
	EventToolCall EventType = "tool_call"
	// EventToolResult is emitted when a tool finishes, with its result or error.
	EventToolResult EventType = "tool_result"
	// EventResponse is emitted when the model replies with text and no tool call.
	EventResponse EventType = "response"
	// EventReflection is emitted for the optional self-review reply.
	EventReflection EventType = "reflection"
	// EventProgress is a human-readable status message.
	EventProgress EventType = "progress"
	// EventError reports a failure. ErrorData.Fatal says whether the loop stopped.
	EventError EventType = "error"
	// EventDone is the last event. DoneData.Reason says why the loop ended.
	EventDone EventType = "done"
	// EventLLMTurn is the audit record of one model call that led to tool calls.
	EventLLMTurn EventType = "llm_turn"
	// EventAwaitingInput is emitted by a pause function that is waiting for a
	// reply. The loop has not ended.
	EventAwaitingInput EventType = "awaiting_input"
)

// Event is one update from a running loop.
type Event struct {
	Type      EventType `json:"type"`
	Data      any       `json:"data"`
	Iteration int       `json:"iteration"`
	Timestamp time.Time `json:"timestamp"`
}

// ThinkingData is the payload of EventThinking.
type ThinkingData struct {
	Message string `json:"message"`
}

// LLMTurnData is the payload of EventLLMTurn.
type LLMTurnData struct {
	TurnIndex              int     `json:"turn_index"`
	InputTokens            int     `json:"input_tokens"`
	OutputTokens           int     `json:"output_tokens"`
	CostAmount             float64 `json:"cost_amount"`
	Provider               string  `json:"provider,omitempty"`
	Model                  string  `json:"model,omitempty"`
	DurationMs             int64   `json:"duration_ms"`
	RequestMessagesJSON    []byte  `json:"request_messages_json,omitempty"`
	ResponseKind           string  `json:"response_kind"`
	AssistantContent       string  `json:"assistant_content,omitempty"`
	AssistantToolCallsJSON []byte  `json:"assistant_tool_calls_json,omitempty"`
	MaxIterations          int     `json:"max_iterations,omitempty"`
	StopCondition          string  `json:"stop_condition,omitempty"`
}

// ToolCallData is the payload of EventToolCall.
type ToolCallData struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
}

// ToolResultData is the payload of EventToolResult.
type ToolResultData struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Result     string `json:"result"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// LLMUsageSummary totals token use over a run. TotalTokens is input plus
// output, plus any totals a provider reported without a split.
type LLMUsageSummary struct {
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
	Model        string `json:"model,omitempty"`
}

// ResponseData is the payload of EventResponse.
type ResponseData struct {
	Content string `json:"content"`
}

// ReflectionData is the payload of EventReflection.
type ReflectionData struct {
	Content string `json:"content"`
}

// ProgressData is the payload of EventProgress.
type ProgressData struct {
	Message string `json:"message"`
	Step    string `json:"step,omitempty"`
}

// ErrorData is the payload of EventError.
type ErrorData struct {
	Error string `json:"error"`
	Fatal bool   `json:"fatal"`
}

// DoneReason says why the loop ended.
type DoneReason string

const (
	// DoneReasonFinished is normal completion.
	DoneReasonFinished DoneReason = "finished"
	// DoneReasonStopped means a stop signal or a cancelled wait ended the run.
	DoneReasonStopped DoneReason = "stopped"
	// DoneReasonMaxIterations means the iteration cap was reached.
	DoneReasonMaxIterations DoneReason = "max_iterations"
	// DoneReasonTimeout means TotalTimeout passed or the context was cancelled.
	DoneReasonTimeout DoneReason = "timeout"
	// DoneReasonError means a fatal error ended the run.
	DoneReasonError DoneReason = "error"
	// DoneReasonAwaitingHuman means a pause function ended the run to wait for
	// a person; the caller resumes later with a new loop.
	DoneReasonAwaitingHuman DoneReason = "awaiting_human"
)

// AwaitingInputData is the payload a pause function emits with
// EventAwaitingInput. Payload carries anything else the caller's UI needs.
type AwaitingInputData struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Payload  any      `json:"payload,omitempty"`
}

// DoneData is the payload of EventDone, emitted exactly once per run.
type DoneData struct {
	Reason       DoneReason `json:"reason"`
	Iterations   int        `json:"iterations"`
	FinalContent string     `json:"final_content,omitempty"`
	// Error is the fatal error message when Reason is DoneReasonError.
	Error string `json:"error,omitempty"`
	// Awaiting is set when Reason is DoneReasonAwaitingHuman.
	Awaiting *AwaitingInputData `json:"awaiting,omitempty"`
	// DroppedEvents counts intermediate events dropped because the event
	// channel was full.
	DroppedEvents int             `json:"dropped_events,omitempty"`
	LLMUsage      LLMUsageSummary `json:"llm_usage"`
}
