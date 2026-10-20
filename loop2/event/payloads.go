package event

// ThinkingData is the payload of Thinking.
type ThinkingData struct {
	Message string `json:"message"`
}

// LLMTurnData is the payload of LLMTurn.
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

// ToolCallData is the payload of ToolCall.
type ToolCallData struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Arguments  string `json:"arguments"`
}

// ToolResultData is the payload of ToolResult.
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

// ResponseData is the payload of Response.
type ResponseData struct {
	Content string `json:"content"`
}

// ReflectionData is the payload of Reflection.
type ReflectionData struct {
	Content string `json:"content"`
}

// ProgressData is the payload of Progress.
type ProgressData struct {
	Message string `json:"message"`
	Step    string `json:"step,omitempty"`
}

// ErrorData is the payload of Error.
type ErrorData struct {
	Error string `json:"error"`
	Fatal bool   `json:"fatal"`
}

// AwaitingInputData is the payload a pause handler emits with AwaitingInput.
// Payload carries anything else the caller's UI needs.
type AwaitingInputData struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Payload  any      `json:"payload,omitempty"`
}

// DoneReason says why the loop ended.
type DoneReason string

const (
	// ReasonFinished is normal completion.
	ReasonFinished DoneReason = "finished"
	// ReasonStopped means a stop signal or a cancelled wait ended the run.
	ReasonStopped DoneReason = "stopped"
	// ReasonMaxIterations means the iteration cap was reached.
	ReasonMaxIterations DoneReason = "max_iterations"
	// ReasonTimeout means TotalTimeout passed or the context was cancelled.
	ReasonTimeout DoneReason = "timeout"
	// ReasonError means a fatal error ended the run.
	ReasonError DoneReason = "error"
	// ReasonAwaitingHuman means a pause handler ended the run to wait for a
	// person; the caller resumes later with a new loop.
	ReasonAwaitingHuman DoneReason = "awaiting_human"
)

// DoneData is the payload of Done, emitted exactly once per run.
type DoneData struct {
	Reason       DoneReason `json:"reason"`
	Iterations   int        `json:"iterations"`
	FinalContent string     `json:"final_content,omitempty"`
	// Error is the fatal error message when Reason is ReasonError.
	Error string `json:"error,omitempty"`
	// Awaiting is set when Reason is ReasonAwaitingHuman.
	Awaiting *AwaitingInputData `json:"awaiting,omitempty"`
	// DroppedEvents counts intermediate events dropped because the event
	// channel was full.
	DroppedEvents int             `json:"dropped_events,omitempty"`
	LLMUsage      LLMUsageSummary `json:"llm_usage"`
}

// Each payload's Type is the label it is sent under.

func (ThinkingData) Type() Type      { return Thinking }
func (ToolCallData) Type() Type      { return ToolCall }
func (ToolResultData) Type() Type    { return ToolResult }
func (ResponseData) Type() Type      { return Response }
func (ReflectionData) Type() Type    { return Reflection }
func (ProgressData) Type() Type      { return Progress }
func (ErrorData) Type() Type         { return Error }
func (DoneData) Type() Type          { return Done }
func (LLMTurnData) Type() Type       { return LLMTurn }
func (AwaitingInputData) Type() Type { return AwaitingInput }
