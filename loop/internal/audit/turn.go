// Package audit builds the LLMTurn record of each model call that led to
// tool calls.
package audit

import (
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop/event"
)

// ModelIdentifier names the provider and model for each turn. It is asked
// once per turn, so it may change during a run.
// → the caller's loop.ModelIdentifier, or defaults.NoIdentity
type ModelIdentifier interface {
	Identity() (provider, model string)
}

// CostEstimator prices one turn. → the caller's loop.CostEstimator, or defaults.ZeroCost
type CostEstimator interface {
	Estimate(provider, model string, inputTokens, outputTokens int) float64
}

// Turn is what happened in one model call.
type Turn struct {
	Index         int
	InputTokens   int
	OutputTokens  int
	Request       []*schema.Message
	Reply         *schema.Message
	DurationMs    int64
	MaxIterations int
}

type Recorder struct {
	identity  ModelIdentifier
	cost      CostEstimator
	stopLabel string
}

func NewRecorder(identity ModelIdentifier, cost CostEstimator, stopLabel string) *Recorder {
	return &Recorder{identity: identity, cost: cost, stopLabel: stopLabel}
}

func (r *Recorder) Record(t Turn) event.LLMTurnData {
	provider, model := r.identity.Identity()
	request, _, _ := SerializeMessages(t.Request)
	return event.LLMTurnData{
		TurnIndex:              t.Index,
		InputTokens:            t.InputTokens,
		OutputTokens:           t.OutputTokens,
		CostAmount:             r.cost.Estimate(provider, model, t.InputTokens, t.OutputTokens),
		Provider:               provider,
		Model:                  model,
		DurationMs:             t.DurationMs,
		RequestMessagesJSON:    request,
		ResponseKind:           responseKind(t.Reply),
		AssistantContent:       t.Reply.Content,
		AssistantToolCallsJSON: SerializeToolCalls(t.Reply),
		MaxIterations:          t.MaxIterations,
		StopCondition:          r.stopLabel,
	}
}
