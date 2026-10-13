package steerloop

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// logRunningMessages logs the messages added since the last call.
func (l *Loop) logRunningMessages(ctx context.Context, iteration int) {
	l.mu.Lock()
	start := l.logged
	if start > len(l.runningMessages) {
		start = 0
	}
	added := make([]*schema.Message, len(l.runningMessages)-start)
	copy(added, l.runningMessages[start:])
	l.logged = len(l.runningMessages)
	l.mu.Unlock()

	if text := formatTranscript(start, added); text != "" {
		l.logger.Log(ctx, l.cfg.TranscriptLogLevel, "agent loop diff",
			slog.Int("iteration", iteration), slog.String("diff", text))
	}
}

func formatTranscript(start int, msgs []*schema.Message) string {
	lines := make([]string, 0, len(msgs))
	for i, m := range msgs {
		if m == nil {
			continue
		}
		body := m.Content
		if body == "" && len(m.ToolCalls) > 0 {
			names := make([]string, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				names[j] = tc.Function.Name
			}
			body = "tool_calls: " + strings.Join(names, ", ")
		}
		lines = append(lines, fmt.Sprintf("%d %s %s", start+i, m.Role, body))
	}
	return strings.Join(lines, "\n")
}

// discardHandler is a slog.Handler that drops every record.
type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (discardHandler) WithAttrs([]slog.Attr) slog.Handler        { return discardHandler{} }
func (discardHandler) WithGroup(string) slog.Handler             { return discardHandler{} }
