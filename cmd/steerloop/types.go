package main

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/mittalkanishk/steerloop"
)

// Every type of the steerloop command, grouped by the part of the CLI it
// serves. Behaviour stays in the file of that part.

// ---------------------------------------------------------------------------
// Entry point: `steerloop <command>` (main.go)
// ---------------------------------------------------------------------------

// env is everything the CLI takes from its surroundings, so tests can supply
// their own.
type env struct {
	stdin           io.Reader
	stdout, stderr  io.Writer
	stdinIsTerminal bool
	getenv          func(string) string
	signals         <-chan os.Signal
}

// ---------------------------------------------------------------------------
// `steerloop run`: flags, tools and the loop (run.go)
// ---------------------------------------------------------------------------

// runOptions is the `steerloop run` command line, as parsed from its flags.
type runOptions struct {
	prompt           string // --prompt, required
	model            string
	manifestPath     string
	systemPromptPath string
	stopName         string
	outputFormat     string
	recordPath       string
	controlPath      string
	maxIterations    int
	maxTokens        int
	stepTimeout      time.Duration
	totalTimeout     time.Duration
	temperature      float64
	askUser          bool
	autoApprove      bool
	verbose          bool
}

// toolset is what the model can call: the manifest's tools, plus ask_user
// and finish when asked for, with the pauses that gate them.
type toolset struct {
	infos    []*schema.ToolInfo
	names    []string // for the run header; the finish tool is not listed
	executor steerloop.ToolExecutor
	pauses   map[string]steerloop.PauseFunc
}

// interruptWatcher maps Ctrl-C onto the run. The first press stops the
// running loop after its current step and a second press aborts outright.
// While the CLI waits at the prompt a single press ends the session.
type interruptWatcher struct {
	running  atomic.Pointer[steerloop.Loop] // nil between turns
	atPrompt atomic.Bool
	aborted  chan struct{}
}

// ---------------------------------------------------------------------------
// Steering a run: terminal lines and the --control file (control.go)
// ---------------------------------------------------------------------------

// controller applies steering input to a running loop.
type controller struct {
	loop   *steerloop.Loop
	stderr io.Writer
}

// controlLine is one line of a --control file.
type controlLine struct {
	Type             string   `json:"type"`
	Message          string   `json:"message"`
	MaxIterations    *int     `json:"max_iterations"`
	Temperature      *float64 `json:"temperature"`
	EnableReflection *bool    `json:"enable_reflection"`
	// WaitMs delays this control, counted from the previous one.
	WaitMs int `json:"wait_ms"`
}

// ---------------------------------------------------------------------------
// Output: the record stream, JSONL and text rendering (render.go, replay.go)
// ---------------------------------------------------------------------------

// record is one line of JSONL output: an event, or the "run" header line.
type record struct {
	Type      string          `json:"type"`
	Iteration int             `json:"iteration"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// runHeader is the data of the first line of a recording.
type runHeader struct {
	Version       string   `json:"version"`
	Model         string   `json:"model"`
	Stop          string   `json:"stop"`
	MaxIterations int      `json:"max_iterations"`
	Tools         []string `json:"tools"`
	Prompt        string   `json:"prompt"`
}

// turnRecord is LLMTurnData with its JSON fields readable instead of base64.
type turnRecord struct {
	TurnIndex          int             `json:"turn_index"`
	InputTokens        int             `json:"input_tokens"`
	OutputTokens       int             `json:"output_tokens"`
	CostAmount         float64         `json:"cost_amount"`
	Provider           string          `json:"provider,omitempty"`
	Model              string          `json:"model,omitempty"`
	DurationMs         int64           `json:"duration_ms"`
	Request            json.RawMessage `json:"request,omitempty"`
	ResponseKind       string          `json:"response_kind"`
	AssistantContent   string          `json:"assistant_content,omitempty"`
	AssistantToolCalls json.RawMessage `json:"assistant_tool_calls,omitempty"`
}

// jsonlWriter writes records as JSON lines. It is safe for concurrent use.
type jsonlWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// textRenderer prints a run for a person: the trace on trace, the final
// content on final. Live runs and replays share it, so they print the same.
type textRenderer struct {
	trace io.Writer
	final io.Writer
	// stop is the run's stop condition, from the header.
	stop string
}
