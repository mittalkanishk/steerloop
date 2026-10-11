package steerloop

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type logEntry struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

// captureHandler is a slog.Handler that keeps every record for assertions.
type captureHandler struct {
	level   slog.Level
	mu      sync.Mutex
	entries []logEntry
}

func (h *captureHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	e := logEntry{level: r.Level, msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		e.attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.entries = append(h.entries, e)
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) all() []logEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]logEntry, len(h.entries))
	copy(out, h.entries)
	return out
}

func (h *captureHandler) count(msg string) int {
	n := 0
	for _, e := range h.all() {
		if e.msg == msg {
			n++
		}
	}
	return n
}

func slogNew(h slog.Handler) *slog.Logger { return slog.New(h) }
func slogLevel(n int) slog.Level          { return slog.Level(n) }

// =============================================================================
// Basic Configuration
// =============================================================================

// toolCall builds a single Eino tool call.
func toolCall(id, name, args string) schema.ToolCall {
	return schema.ToolCall{
		ID:   id,
		Type: "function",
		Function: schema.FunctionCall{
			Name:      name,
			Arguments: args,
		},
	}
}

// basicConfig returns a minimal test config with generous timeouts.
func basicConfig() LoopConfig {
	return LoopConfig{
		MaxIterations:    10,
		StopCondition:    StopOnNoToolCalls,
		Temperature:      0.5,
		StepTimeout:      5 * time.Second,
		TotalTimeout:     10 * time.Second,
		EnableReflection: false,
		StreamEvents:     true,
		DisableStreaming: true,
	}
}

func basicConfigWithHistoryCompactor() LoopConfig {
	historyCompactor := func(msgs []*schema.Message) []*schema.Message {
		return msgs // does nothing just returns the same input slice.
	}

	return LoopConfig{
		MaxIterations:    10,
		StopCondition:    StopOnNoToolCalls,
		Temperature:      0.5,
		StepTimeout:      5 * time.Second,
		TotalTimeout:     10 * time.Second,
		EnableReflection: false,
		StreamEvents:     true,
		DisableStreaming: true,
		HistoryCompactor: historyCompactor,
	}
}

// basicMessages returns a minimal set of initial messages.
func basicMessages() []*schema.Message {
	return []*schema.Message{
		{Role: schema.System, Content: "You are a helpful assistant."},
		{Role: schema.User, Content: "Hello"},
	}
}

// sampleTools returns some test tool definitions.
func sampleTools() []*schema.ToolInfo {

	profile, err := NewToolInfo("get_profile", "Get the user profile", map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	})
	if err != nil {
		panic(err)
	}

	search, err := NewToolInfo("search_content", "Search stored content", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
			},
		},
		"required": []string{"query"},
	})
	if err != nil {
		panic(err)
	}

	return []*schema.ToolInfo{
		profile,
		search,
	}
}

// =============================================================================
// Helpers
// =============================================================================

// collectEvents reads all events from the channel until it's closed or timeout.
func collectEvents(t *testing.T, ch <-chan Event, timeout time.Duration) []Event {
	t.Helper()
	var events []Event
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, evt)
		case <-timer.C:
			t.Fatalf("timeout collecting events (got %d so far)", len(events))
			return events
		}
	}
}

// findEvent returns the first event matching the given type.
func findEvent(events []Event, t EventType) *Event {
	for i := range events {
		if events[i].Type == t {
			return &events[i]
		}
	}
	return nil
}

// findEvents returns all events matching the given type.
func findEvents(events []Event, t EventType) []Event {
	var matched []Event
	for _, e := range events {
		if e.Type == t {
			matched = append(matched, e)
		}
	}
	return matched
}
