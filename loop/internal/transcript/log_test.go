package transcript

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordedLine is one record kept by lineRecorder.
type recordedLine struct {
	msg   string
	attrs map[string]any
}

// lineRecorder is a slog.Handler that keeps every record at or above level.
type lineRecorder struct {
	level slog.Level
	mu    sync.Mutex
	lines []recordedLine
}

func (h *lineRecorder) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *lineRecorder) Handle(_ context.Context, r slog.Record) error {
	line := recordedLine{msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		line.attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.lines = append(h.lines, line)
	h.mu.Unlock()
	return nil
}

func (h *lineRecorder) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *lineRecorder) WithGroup(string) slog.Handler      { return h }

func TestTranscript_LogsTheSeedOnceThenEachDiff(t *testing.T) {
	seed := []*schema.Message{
		{Role: schema.System, Content: "sys"},
		{Role: schema.User, Content: "hi"},
	}
	assert.Equal(t, "0 system sys\n1 user hi", format(0, seed))

	rec := &lineRecorder{level: slog.LevelInfo}
	log := New(slog.New(rec), slog.LevelInfo)
	ctx := context.Background()

	log.Seed(ctx, seed)
	log.Diff(ctx, 1, 2, []*schema.Message{{Role: schema.Assistant, Content: "looked"}})
	log.Diff(ctx, 2, 3, []*schema.Message{{Role: schema.Tool, Content: "outline"}})
	log.Diff(ctx, 3, 4, nil) // nothing new to log

	require.Len(t, rec.lines, 3)
	assert.Equal(t, "agent loop seed", rec.lines[0].msg)
	assert.Equal(t, "0 system sys\n1 user hi", rec.lines[0].attrs["seed"])
	assert.Equal(t, "agent loop diff", rec.lines[1].msg)
	assert.Equal(t, "2 assistant looked", rec.lines[1].attrs["diff"])
	assert.Equal(t, "3 tool outline", rec.lines[2].attrs["diff"])
}

func TestTranscript_NamesToolCallsWhenThereIsNoText(t *testing.T) {
	got := format(5, []*schema.Message{
		nil,
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{
			{Function: schema.FunctionCall{Name: "lookup"}},
			{Function: schema.FunctionCall{Name: "ping"}},
		}},
	})
	assert.Equal(t, "6 assistant tool_calls: lookup, ping", got, "nil messages are skipped but keep their index")
}
