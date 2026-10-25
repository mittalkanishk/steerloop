package loop_test

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop"
	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/steerlooptest"
)

// The serializers behind LLMTurnData (request JSON, tool-call JSON, reply
// kind and the size cap) are tested in loop/internal/audit; transcript
// formatting in loop/internal/transcript. This file tests what a caller
// sees: the llm_turn event, usage totals and the transcript log level.

// =============================================================================
// Audit and usage
// =============================================================================

// fixedIdentity names one provider and model for every turn.
type fixedIdentity struct{ provider, model string }

func (f fixedIdentity) Identity() (string, string) { return f.provider, f.model }

// recordingPricer prices a turn at 0.01 per input and 0.1 per output token,
// and remembers what it was asked to price.
type recordingPricer struct{ provider, model string }

func (p *recordingPricer) Estimate(provider, modelName string, in, out int) float64 {
	p.provider, p.model = provider, modelName
	return float64(in)*0.01 + float64(out)*0.1
}

func (s *LoopSuite) TestAudit_TheLLMTurnEventCarriesUsageIdentityAndCost() {
	steps := []steerlooptest.Step{
		{ToolCalls: []schema.ToolCall{profileCall()}, Usage: &schema.TokenUsage{PromptTokens: 100, CompletionTokens: 20}},
		{Content: "answer", Usage: &schema.TokenUsage{PromptTokens: 150, CompletionTokens: 30}},
	}

	events, _ := s.runScript(steps...)
	turn := payload[event.LLMTurnData](s, events)
	s.Equal(1, turn.TurnIndex)
	s.Equal(100, turn.InputTokens)
	s.Equal(20, turn.OutputTokens)
	s.Zero(turn.CostAmount)
	s.Empty(turn.Provider)
	s.Equal("tool_calls", turn.ResponseKind)
	s.Equal("no_tool_finish", turn.StopCondition)
	s.Equal(10, turn.MaxIterations)
	s.JSONEq(`[{"id":"call_1","type":"function","function":{"name":"get_profile","arguments":"{}"}}]`, string(turn.AssistantToolCallsJSON))
	var request struct {
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	s.Require().NoError(json.Unmarshal(turn.RequestMessagesJSON, &request))
	s.Require().Len(request.Messages, 3, "the seed system message, the stop instructions, the user message")
	s.Equal("system", request.Messages[1].Role, "the stop instructions follow the caller's system message")
	s.Equal("user", request.Messages[2].Role)
	s.Equal(event.LLMUsageSummary{InputTokens: 250, OutputTokens: 50, TotalTokens: 300}, s.done(events).LLMUsage)

	pricer := &recordingPricer{}
	s.cfg.ModelIdentity = fixedIdentity{provider: "acme", model: "big-1"}
	s.cfg.CostEstimator = pricer
	events, _ = s.runScript(steps...)
	turn = payload[event.LLMTurnData](s, events)
	s.Equal("acme", turn.Provider)
	s.Equal("big-1", turn.Model)
	s.Equal("acme", pricer.provider)
	s.Equal("big-1", pricer.model)
	s.InDelta(3.0, turn.CostAmount, 0.0001)
}

func (s *LoopSuite) TestAudit_EachStopConditionHasAStableLabel() {
	for cond, label := range map[loop.StopCondition]string{
		loop.StopOnNoToolFinish: "no_tool_finish",
		loop.StopOnFinishTool:   "finish_tool",
		loop.StopOnExternal:     "external_finish",
	} {
		s.cfg.StopCondition = cond
		events, _ := s.runScript(asksFor(profileCall()), says("done"))
		s.Equal(label, payload[event.LLMTurnData](s, events).StopCondition)
	}
}

func (s *LoopSuite) TestUsage_CountsProvidersThatReportOnlyATotal() {
	events, _ := s.runScript(steerlooptest.Step{Content: "hi", Usage: &schema.TokenUsage{TotalTokens: 42}})
	s.Equal(event.LLMUsageSummary{TotalTokens: 42}, s.done(events).LLMUsage)
}

// =============================================================================
// Transcript logging
// =============================================================================

func (s *LoopSuite) TestTranscript_IsLoggedAtTheConfiguredLevel() {
	s.cfg.TranscriptLogLevel = slog.LevelDebug // below the handler's info level
	s.runScript(says("hi"))
	s.Zero(s.logs.count("agent loop seed"))

	s.logs = &captureHandler{level: slog.LevelInfo}
	s.cfg.TranscriptLogLevel = slog.LevelInfo
	s.runScript(says("hi"))
	s.Equal(1, s.logs.count("agent loop seed"))
	s.Equal(1, s.logs.count("agent loop diff"))
}

// =============================================================================
// Tools
// =============================================================================

func (s *LoopSuite) TestTool_NewToolInfoBuildsADefinitionFromJSONSchema() {
	info, err := loop.NewToolInfo("ping", "Ping", nil)
	s.Require().NoError(err)
	s.Equal("ping", info.Name)
	s.Nil(info.ParamsOneOf, "nil params mean no parameters")

	info, err = loop.NewToolInfo("search", "Search", map[string]any{
		"type":       "object",
		"properties": map[string]any{"q": map[string]any{"type": "string"}},
		"required":   []string{"q"},
	})
	s.Require().NoError(err)
	js, err := info.ToJSONSchema()
	s.Require().NoError(err)
	s.Equal([]string{"q"}, js.Required)

	_, err = loop.NewToolInfo("bad", "", map[string]any{"type": make(chan int)})
	s.ErrorContains(err, "marshal params")
	_, err = loop.NewToolInfo("bad", "", map[string]any{"properties": "not an object"})
	s.ErrorContains(err, "parse params")
}

// =============================================================================
// Fuzz targets. Go requires these to be top-level functions; CI runs them by name.
// =============================================================================

func FuzzNewToolInfo(f *testing.F) {
	f.Add("search", `{"type":"object","properties":{"q":{"type":"string"}}}`)
	f.Add("", `{}`)
	f.Add("x", `{"properties":7}`)
	f.Fuzz(func(t *testing.T, name, schemaJSON string) {
		var params map[string]any
		if err := json.Unmarshal([]byte(schemaJSON), &params); err != nil {
			return
		}
		_, _ = loop.NewToolInfo(name, "desc", params) // must not panic
	})
}
