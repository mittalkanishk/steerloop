package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Kanishkmittal55/steerloop"
)

// record is one line of JSONL output: an event, or the "run" header line.
type record struct {
	Type      string          `json:"type"`
	Iteration int             `json:"iteration"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

const recordTypeRun = "run"

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

func toRecord(evt steerloop.Event) (record, error) {
	payload := evt.Data
	if turn, ok := evt.Data.(steerloop.LLMTurnData); ok {
		payload = turnRecord{
			TurnIndex:          turn.TurnIndex,
			InputTokens:        turn.InputTokens,
			OutputTokens:       turn.OutputTokens,
			CostAmount:         turn.CostAmount,
			Provider:           turn.Provider,
			Model:              turn.Model,
			DurationMs:         turn.DurationMs,
			Request:            turn.RequestMessagesJSON,
			ResponseKind:       turn.ResponseKind,
			AssistantContent:   turn.AssistantContent,
			AssistantToolCalls: turn.AssistantToolCallsJSON,
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return record{}, err
	}
	return record{Type: string(evt.Type), Iteration: evt.Iteration, Timestamp: evt.Timestamp, Data: data}, nil
}

func headerRecord(h runHeader) record {
	data, _ := json.Marshal(h)
	return record{Type: recordTypeRun, Timestamp: time.Now().UTC(), Data: data}
}

// jsonlWriter writes records as JSON lines. It is safe for concurrent use.
type jsonlWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (j *jsonlWriter) write(r record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	_, err = j.w.Write(append(line, '\n'))
	return err
}

// textRenderer prints a run for a person: the trace on trace, the final
// content on final. Live runs and replays share it, so they print the same.
type textRenderer struct {
	trace io.Writer
	final io.Writer
	// stop is the run's stop condition, from the header.
	stop string
}

const maxResultPreview = 240

func (t *textRenderer) render(r record) {
	switch r.Type {
	case recordTypeRun:
		var h runHeader
		if json.Unmarshal(r.Data, &h) == nil {
			t.stop = h.Stop
			fmt.Fprintf(t.trace, "model %s · stop %s · max %d steps\n", h.Model, h.Stop, h.MaxIterations)
		}

	case string(steerloop.EventThinking):
		fmt.Fprintf(t.trace, "[step %d]\n", r.Iteration)

	case string(steerloop.EventToolCall):
		var d steerloop.ToolCallData
		if json.Unmarshal(r.Data, &d) == nil {
			fmt.Fprintf(t.trace, "  → %s %s\n", d.Name, oneLine(d.Arguments, maxResultPreview))
		}

	case string(steerloop.EventToolResult):
		var d steerloop.ToolResultData
		if json.Unmarshal(r.Data, &d) == nil {
			mark := "←"
			if d.Error != "" {
				mark = "✗"
			}
			fmt.Fprintf(t.trace, "  %s %s: %s\n", mark, d.Name, oneLine(d.Result, maxResultPreview))
		}

	case string(steerloop.EventResponse):
		// When the loop stops on a plain reply, that reply is the final
		// content and is printed once, at the end.
		if t.stop == "no-tool-calls" {
			return
		}
		var d steerloop.ResponseData
		if json.Unmarshal(r.Data, &d) == nil {
			fmt.Fprintf(t.trace, "  assistant: %s\n", oneLine(d.Content, maxResultPreview))
		}

	case string(steerloop.EventReflection):
		var d steerloop.ReflectionData
		if json.Unmarshal(r.Data, &d) == nil {
			fmt.Fprintf(t.trace, "  reflection: %s\n", oneLine(d.Content, maxResultPreview))
		}

	case string(steerloop.EventProgress):
		var d steerloop.ProgressData
		if json.Unmarshal(r.Data, &d) == nil {
			fmt.Fprintf(t.trace, "  · %s\n", d.Message)
		}

	case string(steerloop.EventAwaitingInput):
		var d steerloop.AwaitingInputData
		if json.Unmarshal(r.Data, &d) == nil {
			options := ""
			if len(d.Options) > 0 {
				options = " [" + strings.Join(d.Options, " / ") + "]"
			}
			fmt.Fprintf(t.trace, "  ? %s%s\n", d.Question, options)
		}

	case string(steerloop.EventError):
		var d steerloop.ErrorData
		if json.Unmarshal(r.Data, &d) == nil {
			fmt.Fprintf(t.trace, "  error: %s\n", d.Error)
		}

	case string(steerloop.EventDone):
		var d steerloop.DoneData
		if json.Unmarshal(r.Data, &d) != nil {
			return
		}
		summary := fmt.Sprintf("done: %s · %d step(s) · %d tokens", d.Reason, d.Iterations, d.LLMUsage.TotalTokens)
		if d.DroppedEvents > 0 {
			summary += fmt.Sprintf(" · %d event(s) dropped", d.DroppedEvents)
		}
		fmt.Fprintln(t.trace, summary)
		if d.FinalContent != "" && d.Reason != steerloop.DoneReasonAwaitingHuman {
			fmt.Fprintln(t.final, d.FinalContent)
		}
	}
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if runes := []rune(s); len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return s
}

// readRecords decodes a JSONL recording.
func readRecords(r io.Reader) ([]record, error) {
	var out []record
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var rec record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if rec.Type == "" {
			return nil, fmt.Errorf("line %d: missing \"type\"", line)
		}
		out = append(out, rec)
	}
	return out, sc.Err()
}
