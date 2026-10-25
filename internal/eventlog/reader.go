package eventlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mittalkanishk/steerloop/loop/event"
)

// maxLine caps one recorded event. A tool result or an audit record can be
// large, so this is well above bufio's 64 KiB default.
const maxLine = 16 * 1024 * 1024

// record is one line of a recording before its payload is decoded. Data
// stays raw until Type says which payload struct it is.
type record struct {
	Type      event.Type      `json:"type"`
	Data      json.RawMessage `json:"data"`
	Iteration int             `json:"iteration"`
	Timestamp time.Time       `json:"timestamp"`
}

// Read decodes a recording written by JSONL. Blank lines are skipped. Each
// event keeps its recorded Iteration and Timestamp. The first bad line ends
// the read, and the error names its line number.
func Read(r io.Reader) ([]event.Event, error) {
	var events []event.Event
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	for line := 1; sc.Scan(); line++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		evt, err := decodeEvent(sc.Bytes())
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		events = append(events, evt)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

// decodeEvent decodes one line. It does not use event.New, which would
// stamp the current time over the recorded one.
func decodeEvent(line []byte) (event.Event, error) {
	var rec record
	if err := json.Unmarshal(line, &rec); err != nil {
		return event.Event{}, err
	}
	if rec.Type == "" {
		return event.Event{}, errors.New(`missing "type"`)
	}
	if len(rec.Data) == 0 {
		return event.Event{}, fmt.Errorf("%s: missing \"data\"", rec.Type)
	}
	data, err := decodeData(rec.Type, rec.Data)
	if err != nil {
		return event.Event{}, fmt.Errorf("%s: %w", rec.Type, err)
	}
	return event.Event{
		Type:      rec.Type,
		Data:      data,
		Iteration: rec.Iteration,
		Timestamp: rec.Timestamp,
	}, nil
}

// decodeData picks the payload struct for t. Payloads are values, not
// pointers, as the loop emits them, so a type switch on event.DoneData
// matches a replayed event the same as a live one.
func decodeData(t event.Type, raw json.RawMessage) (event.Data, error) {
	switch t {
	case event.Thinking:
		return decode[event.ThinkingData](raw)
	case event.ToolCall:
		return decode[event.ToolCallData](raw)
	case event.ToolResult:
		return decode[event.ToolResultData](raw)
	case event.Response:
		return decode[event.ResponseData](raw)
	case event.Reflection:
		return decode[event.ReflectionData](raw)
	case event.Progress:
		return decode[event.ProgressData](raw)
	case event.Error:
		return decode[event.ErrorData](raw)
	case event.Done:
		return decode[event.DoneData](raw)
	case event.LLMTurn:
		return decode[event.LLMTurnData](raw)
	case event.AwaitingInput:
		return decode[event.AwaitingInputData](raw)
	default:
		return nil, errors.New("unknown event type")
	}
}

func decode[T event.Data](raw json.RawMessage) (event.Data, error) {
	var d T
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, err
	}
	return d, nil
}
