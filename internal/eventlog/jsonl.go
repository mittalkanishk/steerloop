// Package eventlog writes a run's events as JSON lines, for other programs
// and for recordings.
package eventlog

import (
	"encoding/json"
	"io"
	"sync"

	"github.com/mittalkanishk/steerloop/loop2/event"
)

// JSONL writes each event as one JSON line. It is safe for concurrent use.
type JSONL struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func NewJSONL(w io.Writer) *JSONL {
	return &JSONL{enc: json.NewEncoder(w)}
}

func (j *JSONL) Write(evt event.Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	_ = j.enc.Encode(evt)
}
