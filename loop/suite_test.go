package loop_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/suite"
	"go.uber.org/goleak"

	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/loop/pause"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// LoopSuite covers loop through its public API: the loop, its controls,
// its event stream, its stop and reflection policies. Every test starts from
// a fresh configuration, executor and log capture, so a test states only
// what is particular to its own scenario.
type LoopSuite struct {
	suite.Suite

	// cfg is the configuration every loop in the test is built with. A test
	// changes the fields its scenario needs before building a loop.
	cfg loop.Config
	// exec runs the tools. It answers "result from <tool>" until a test
	// replaces it with an executor that answers differently.
	exec *steerlooptest.RecordingExecutor
	// logs receives every line the loop logs.
	logs *captureHandler
}

func TestLoopSuite(t *testing.T) {
	suite.Run(t, new(LoopSuite))
}

func (s *LoopSuite) SetupTest() {
	s.cfg = loop.Config{
		MaxIterations:    10,
		StopCondition:    loop.StopOnNoToolFinish,
		Temperature:      0.5,
		IterationTimeout: 5 * time.Second,
		TotalTimeout:     10 * time.Second,
		StreamEvents:     true,
		DisableStreaming: true,
	}
	s.exec = steerlooptest.NewRecordingExecutor(nil)
	s.logs = &captureHandler{level: slog.LevelInfo}
}

// =============================================================================
// Fixtures
// =============================================================================

const (
	// eventTimeout bounds how long a test waits for a run to end.
	eventTimeout = 15 * time.Second

	profileTool = "get_profile"
	searchTool  = "search_content"
	askUserTool = "ask_user"
)

// seedConversation is the conversation every run starts from. The loop adds
// its stop instructions as a second system message (Engine.Seed), so the
// model sees three messages on its first call.
func seedConversation() []*schema.Message {
	return []*schema.Message{
		{Role: schema.System, Content: "You are a helpful assistant."},
		{Role: schema.User, Content: "Hello"},
	}
}

// says is a model turn that answers with text.
func says(text string) steerlooptest.Step {
	return steerlooptest.Step{Content: text}
}

// asksFor is a model turn that asks for tools.
func asksFor(toolCalls ...schema.ToolCall) steerlooptest.Step {
	return steerlooptest.Step{ToolCalls: toolCalls}
}

// fails is a model turn whose call returns err.
func fails(err error) steerlooptest.Step {
	return steerlooptest.Step{Err: err}
}

// profileCall is a call to the profile tool.
func profileCall() schema.ToolCall {
	return steerlooptest.ToolCall("call_1", profileTool, `{}`)
}

// finishCall ends a StopOnFinishTool run with args as its result.
func finishCall(args string) schema.ToolCall {
	return steerlooptest.ToolCall("call_finish", loop.FinishToolName, args)
}

// sampleTools are the tool definitions a test binds to the model.
func (s *LoopSuite) sampleTools() []*schema.ToolInfo {
	profile, err := loop.NewToolInfo(profileTool, "Get the user profile", map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	})
	s.Require().NoError(err)

	search, err := loop.NewToolInfo(searchTool, "Search stored content", map[string]any{
		"type":       "object",
		"properties": map[string]any{"query": map[string]any{"type": "string"}},
		"required":   []string{"query"},
	})
	s.Require().NoError(err)

	return []*schema.ToolInfo{profile, search}
}

// newLoop builds a loop from the suite's configuration, executor and logs.
func (s *LoopSuite) newLoop(llm model.ToolCallingChatModel, tools ...*schema.ToolInfo) *loop.Loop {
	s.T().Helper()
	loop, err := loop.New(s.cfg, llm, tools, s.exec, slog.New(s.logs))
	s.Require().NoError(err)
	return loop
}

// run starts loop from the seed conversation and returns everything it emits.
func (s *LoopSuite) run(loop *loop.Loop) []event.Event {
	s.T().Helper()
	return s.collect(loop.Start(context.Background(), seedConversation()))
}

// runScript runs a loop against a model that plays steps in order.
func (s *LoopSuite) runScript(steps ...steerlooptest.Step) ([]event.Event, *steerlooptest.ScriptedModel) {
	s.T().Helper()
	llm := steerlooptest.NewScriptedModel(steps...)
	return s.run(s.newLoop(llm)), llm
}

// collect reads events until the channel is closed.
func (s *LoopSuite) collect(events <-chan event.Event) []event.Event {
	s.T().Helper()
	timeout := time.NewTimer(eventTimeout)
	defer timeout.Stop()

	var all []event.Event
	for {
		select {
		case evt, ok := <-events:
			if !ok {
				return all
			}
			all = append(all, evt)
		case <-timeout.C:
			s.Require().FailNowf("the run did not end", "collected %d events", len(all))
		}
	}
}

// done returns the payload of the run's single done event.
func (s *LoopSuite) done(events []event.Event) event.DoneData {
	s.T().Helper()
	dones := payloads[event.DoneData](events)
	s.Require().Len(dones, 1, "a run has exactly one done event")
	return dones[0]
}

// payload returns the first payload of type T.
func payload[T event.Data](s *LoopSuite, events []event.Event) T {
	s.T().Helper()
	all := payloads[T](events)
	var zero T
	s.Require().NotEmpty(all, "no %s event was emitted", zero.Type())
	return all[0]
}

// payloads returns every payload of type T, in order.
func payloads[T event.Data](events []event.Event) []T {
	var out []T
	for _, evt := range events {
		if d, ok := evt.Data.(T); ok {
			out = append(out, d)
		}
	}
	return out
}

// eventsOfType returns the events labelled t, in order.
func eventsOfType(events []event.Event, t event.Type) []event.Event {
	var out []event.Event
	for _, evt := range events {
		if evt.Type == t {
			out = append(out, evt)
		}
	}
	return out
}

// lastMessage is the newest message the model saw on its most recent call.
func lastMessage(llm *steerlooptest.ScriptedModel) *schema.Message {
	msgs := llm.LastMessages()
	return msgs[len(msgs)-1]
}

// heldModel answers every call with text but holds its first call open until
// release is called. A test uses it to act on a loop that is known to be
// inside a model call.
func heldModel(text string) (llm *steerlooptest.ScriptedModel, release func()) {
	gate := make(chan struct{})
	var first, once sync.Once
	llm = &steerlooptest.ScriptedModel{
		Handler: func(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
			first.Do(func() { <-gate })
			return &schema.Message{Role: schema.Assistant, Content: text}, nil
		},
	}
	return llm, func() { once.Do(func() { close(gate) }) }
}

// waitForModelCall returns once the loop is inside its first model call.
func (s *LoopSuite) waitForModelCall(llm *steerlooptest.ScriptedModel) {
	s.T().Helper()
	s.Require().Eventually(func() bool { return llm.CallCount() > 0 }, eventTimeout, time.Millisecond)
}

// pauseFunc lets a test write a pause.Handler inline. Test code only: the
// library and the CLI use named handler types.
type pauseFunc func(ctx context.Context, call pause.Call, s pause.Session) (string, bool)

func (f pauseFunc) Handle(ctx context.Context, call pause.Call, s pause.Session) (string, bool) {
	return f(ctx, call, s)
}

// pausingLoop builds a loop whose first model turn calls the ask_user pause
// tool and whose second turn says "thanks".
func (s *LoopSuite) pausingLoop(h pause.Handler) (*loop.Loop, *steerlooptest.ScriptedModel) {
	s.T().Helper()
	llm := steerlooptest.NewScriptedModel(
		asksFor(steerlooptest.ToolCall("call_1", askUserTool, `{"question":"colour?"}`)),
		says("thanks"),
	)
	s.cfg.Pauses = map[string]pause.Handler{askUserTool: h}
	return s.newLoop(llm), llm
}

// blockedPausingLoop builds a pausing loop whose pause waits for an inject.
// blocked is closed once the pause is waiting.
func (s *LoopSuite) blockedPausingLoop() (loop *loop.Loop, llm *steerlooptest.ScriptedModel, blocked <-chan struct{}) {
	s.T().Helper()
	waiting := make(chan struct{})
	loop, llm = s.pausingLoop(pauseFunc(func(ctx context.Context, _ pause.Call, ps pause.Session) (string, bool) {
		close(waiting)
		return ps.AwaitReply(ctx)
	}))
	return loop, llm, waiting
}

// failingBinder is a model whose WithTools fails, or returns no model.
type failingBinder struct{ err error }

func (failingBinder) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, nil
}

func (failingBinder) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func (f failingBinder) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return nil, f.err
}

// countingModel counts how often each model entry point is used.
type countingModel struct {
	inner            *steerlooptest.ScriptedModel
	generate, stream int
}

func (c *countingModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	c.generate++
	return c.inner.Generate(ctx, in, opts...)
}

func (c *countingModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	c.stream++
	return c.inner.Stream(ctx, in, opts...)
}

func (c *countingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return c, nil
}

// captureHandler is a slog.Handler that keeps every record for assertions.
type captureHandler struct {
	level   slog.Level
	mu      sync.Mutex
	entries []logEntry
}

type logEntry struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

func (h *captureHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	entry := logEntry{level: r.Level, msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool {
		entry.attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.entries = append(h.entries, entry)
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func (h *captureHandler) all() []logEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]logEntry(nil), h.entries...)
}

// count returns how many records carry msg.
func (h *captureHandler) count(msg string) int {
	n := 0
	for _, entry := range h.all() {
		if entry.msg == msg {
			n++
		}
	}
	return n
}
