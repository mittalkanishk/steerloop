package state

import (
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/event"
)

// Usage is the token use of a run so far.
type Usage struct {
	Input     int
	Output    int
	TotalOnly int // totals a provider reported without an input/output split
	Model     string
}

func (u Usage) Summary() event.LLMUsageSummary {
	return event.LLMUsageSummary{
		InputTokens:  u.Input,
		OutputTokens: u.Output,
		TotalTokens:  u.Input + u.Output + u.TotalOnly,
		Model:        u.Model,
	}
}

// Usage returns a snapshot of the token use so far.
func (r *Run) Usage() Usage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.usage
}

// AddUsage adds the token usage reported on an assistant message.
func (r *Run) AddUsage(msg *schema.Message) {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return
	}
	u := msg.ResponseMeta.Usage
	r.mu.Lock()
	defer r.mu.Unlock()
	if u.PromptTokens != 0 || u.CompletionTokens != 0 {
		r.usage.Input += u.PromptTokens
		r.usage.Output += u.CompletionTokens
	} else if u.TotalTokens != 0 {
		r.usage.TotalOnly += u.TotalTokens
	}
}
