// Package transcript logs the conversation as it grows. These lines contain
// prompt and reply text, so they go at a level of the caller's choosing.
package transcript

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/schema"
)

type Logger struct {
	logger *slog.Logger
	level  slog.Level
}

func New(logger *slog.Logger, level slog.Level) *Logger {
	return &Logger{logger: logger, level: level}
}

// Seed logs the starting messages.
func (t *Logger) Seed(ctx context.Context, msgs []*schema.Message) {
	if text := format(0, msgs); text != "" {
		t.logger.Log(ctx, t.level, "agent loop seed", slog.String("seed", text))
	}
}

// Diff logs the messages added during one iteration.
func (t *Logger) Diff(ctx context.Context, iteration, start int, added []*schema.Message) {
	if text := format(start, added); text != "" {
		t.logger.Log(ctx, t.level, "agent loop diff", slog.Int("iteration", iteration), slog.String("diff", text))
	}
}

func format(start int, msgs []*schema.Message) string {
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
