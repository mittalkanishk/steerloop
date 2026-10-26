package loop_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/loop/pause"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// Helpers for the loop test suite in suite_test.go: the fixtures every test
// shares, then the fakes and builders each section of the suite uses.

// =============================================================================
// Fixtures shared by every test
// =============================================================================

// Names and limits the tests share.
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

// Handle calls f.
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

// Generate is never reached: New fails first, in WithTools.
func (failingBinder) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, nil
}

// Stream is never reached either.
func (failingBinder) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

// WithTools returns f.err and no model.
func (f failingBinder) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return nil, f.err
}

// countingModel counts how often each model entry point is used.
type countingModel struct {
	inner            *steerlooptest.ScriptedModel
	generate, stream int
}

// Generate counts the call and plays the next scripted turn.
func (c *countingModel) Generate(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	c.generate++
	return c.inner.Generate(ctx, in, opts...)
}

// Stream counts the call and streams the next scripted turn.
func (c *countingModel) Stream(ctx context.Context, in []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	c.stream++
	return c.inner.Stream(ctx, in, opts...)
}

// WithTools returns c itself, so the counts survive tool binding.
func (c *countingModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return c, nil
}

// captureHandler is a slog.Handler that keeps every record for assertions.
type captureHandler struct {
	level   slog.Level
	mu      sync.Mutex
	entries []logEntry
}

// logEntry is one captured log record.
type logEntry struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

// Enabled keeps records at h.level and above.
func (h *captureHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

// Handle stores the record with its attributes.
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

// WithAttrs and WithGroup return h unchanged: the loop's logger adds no
// attributes or groups that a test asserts on.
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// all returns a copy of every record so far.
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

// =============================================================================
// Helpers for the configuration tests
// =============================================================================

// maxTokensOptions is a CallOptionsBuilder that records the temperature it
// was given and sends only a max-tokens option.
type maxTokensOptions struct {
	temperatureSeen float32
}

// Build records temperature and returns only the max-tokens option.
func (o *maxTokensOptions) Build(temperature float32) []model.Option {
	o.temperatureSeen = temperature
	return []model.Option{model.WithMaxTokens(77)}
}

// keepNewest is a HistoryCompactor that keeps only the newest message.
type keepNewest struct{}

// Compact drops everything but the last message.
func (keepNewest) Compact(msgs []*schema.Message) []*schema.Message {
	return msgs[len(msgs)-1:]
}

// =============================================================================
// Helpers for the stop policy tests
// =============================================================================

// stopInstructionsSeen is the system message the loop placed after the
// caller's own on the model's first call.
func stopInstructionsSeen(llm *steerlooptest.ScriptedModel) *schema.Message {
	return llm.Calls()[0][1]
}

// =============================================================================
// Helpers for the control tests
// =============================================================================

// controlQueueSize is the capacity of the loop's control queue
// (loop/internal/control.queueSize), which tests outside the package
// cannot read.
const controlQueueSize = 8

// =============================================================================
// Helpers for the events tests
// =============================================================================

// typeRecorder is an EventSink that keeps the type of every event it sees.
type typeRecorder struct {
	mu    sync.Mutex
	types []event.Type
}

// Write records the event's type.
func (r *typeRecorder) Write(evt event.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.types = append(r.types, evt.Type)
}

// seen returns a copy of the types recorded so far, in order.
func (r *typeRecorder) seen() []event.Type {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event.Type(nil), r.types...)
}

// =============================================================================
// Helpers for the retry and streaming tests
// =============================================================================

// rateLimitedOnly is an ErrorClassifier that retries only "rate limited".
type rateLimitedOnly struct{}

// IsTransient is true only for the "rate limited" error.
func (rateLimitedOnly) IsTransient(err error) bool {
	return err != nil && err.Error() == "rate limited"
}

// =============================================================================
// Helpers for the reflection tests
// =============================================================================

// verdictTool is the tool the judge must call. It copies
// reflection.VerdictTool, because the suite uses only the public API.
const verdictTool = "review_verdict"

// rules is a judge turn that returns a verdict.
func rules(verdict, reason, hint string) steerlooptest.Step {
	args, _ := json.Marshal(map[string]any{"verdict": verdict, "reason": reason, "hint": hint, "confidence": 0.9})
	return asksFor(steerlooptest.ToolCall("call_verdict", verdictTool, string(args)))
}

// passes is a judge turn that passes the answer.
func passes() steerlooptest.Step { return rules("pass", "", "") }

// judgeBrief is the user message of the judge call at index i.
func judgeBrief(llm *steerlooptest.ScriptedModel, i int) string {
	call := llm.Calls()[i]
	return call[len(call)-1].Content
}

// =============================================================================
// Helpers for the audit and usage tests
// =============================================================================

// fixedIdentity names one provider and model for every turn.
type fixedIdentity struct{ provider, model string }

// Identity returns the fixed provider and model.
func (f fixedIdentity) Identity() (string, string) { return f.provider, f.model }

// recordingPricer prices a turn at 0.01 per input and 0.1 per output token,
// and remembers what it was asked to price.
type recordingPricer struct{ provider, model string }

// Estimate remembers provider and model and prices the turn.
func (p *recordingPricer) Estimate(provider, modelName string, in, out int) float64 {
	p.provider, p.model = provider, modelName
	return float64(in)*0.01 + float64(out)*0.1
}
