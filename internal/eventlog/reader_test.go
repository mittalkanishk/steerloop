package eventlog

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mittalkanishk/steerloop/loop/event"
)

// TestReadRoundTrip writes one event of every type with JSONL and reads it
// back unchanged, recorded Iteration and Timestamp included.
func TestReadRoundTrip(t *testing.T) {
	at := time.Date(2026, 10, 25, 12, 0, 0, 123456789, time.UTC)
	payloads := []event.Data{
		event.ThinkingData{Message: "Reasoning (iteration 1/12)…"},
		event.LLMTurnData{TurnIndex: 1, InputTokens: 10, OutputTokens: 5, ResponseKind: "tool_calls", RequestMessagesJSON: []byte(`[{"role":"user"}]`)},
		event.ToolCallData{ToolCallID: "c1", Name: "count_lines", Arguments: `{"path":"go.mod"}`},
		event.ToolResultData{ToolCallID: "c1", Name: "count_lines", Result: "12 go.mod", DurationMs: 3},
		event.ResponseData{Content: "go.mod has 12 lines."},
		event.ReflectionData{Mode: event.ReflectOnFinish, Verdict: "fail", Reason: "drift", Hint: "fix it", Confidence: 0.8},
		event.ProgressData{Message: "Configuration updated", Step: "config_modify"},
		event.ErrorData{Error: "tool panicked", Fatal: false},
		event.AwaitingInputData{Question: "Run make_dir?", Options: []string{"y", "n"}},
		event.DoneData{
			Reason:       event.ReasonAwaitingHuman,
			Iterations:   2,
			FinalContent: "go.mod has 12 lines.",
			Awaiting:     &event.AwaitingInputData{Question: "Run make_dir?"},
			LLMUsage:     event.LLMUsageSummary{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
		},
	}

	var want []event.Event
	var buf bytes.Buffer
	w := NewJSONL(&buf)
	for i, d := range payloads {
		evt := event.Event{Type: d.Type(), Data: d, Iteration: i, Timestamp: at.Add(time.Duration(i) * time.Second)}
		want = append(want, evt)
		w.Write(evt)
	}

	got, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the events\n got: %+v\nwant: %+v", got, want)
	}
}

func TestReadSkipsBlankLines(t *testing.T) {
	in := "\n" +
		`{"type":"response","data":{"content":"hi"},"iteration":1,"timestamp":"2026-10-25T12:00:00Z"}` + "\n" +
		"   \n" +
		`{"type":"done","data":{"reason":"finished","iterations":1,"llm_usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}},"iteration":1,"timestamp":"2026-10-25T12:00:01Z"}` + "\n"

	got, err := Read(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if _, ok := got[1].Data.(event.DoneData); !ok {
		t.Fatalf("second event data is %T, want event.DoneData", got[1].Data)
	}
}

func TestReadEmpty(t *testing.T) {
	got, err := Read(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d events, want none", len(got))
	}
}

func TestReadRejectsBadLines(t *testing.T) {
	good := `{"type":"response","data":{"content":"hi"},"iteration":1,"timestamp":"2026-10-25T12:00:00Z"}`
	for name, tc := range map[string]struct {
		line string
		want string
	}{
		"not json":      {`{not json`, "line 2: invalid character"},
		"missing type":  {`{"data":{"content":"hi"}}`, `line 2: missing "type"`},
		"missing data":  {`{"type":"response"}`, `line 2: response: missing "data"`},
		"unknown type":  {`{"type":"telemetry","data":{}}`, "line 2: telemetry: unknown event type"},
		"wrong payload": {`{"type":"done","data":{"iterations":"two"}}`, "line 2: done: json: cannot unmarshal"},
	} {
		_, err := Read(strings.NewReader(good + "\n" + tc.line + "\n"))
		if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: got error %v, want prefix %q", name, err, tc.want)
		}
	}
}
