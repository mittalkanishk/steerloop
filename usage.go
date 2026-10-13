package steerloop

import (
	"github.com/cloudwego/eino/schema"
)

type llmUsageAccumulator struct {
	input     int
	output    int
	totalOnly int
	model     string
}

func (acc llmUsageAccumulator) summary() LLMUsageSummary {
	return LLMUsageSummary{
		InputTokens:  acc.input,
		OutputTokens: acc.output,
		TotalTokens:  acc.input + acc.output + acc.totalOnly,
		Model:        acc.model,
	}
}

func (l *Loop) usageSnapshot() llmUsageAccumulator {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.usage
}

// addUsage adds the token usage reported on an assistant message.
func (l *Loop) addUsage(msg *schema.Message) {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return
	}
	u := msg.ResponseMeta.Usage
	l.mu.Lock()
	defer l.mu.Unlock()
	if u.PromptTokens != 0 || u.CompletionTokens != 0 {
		l.usage.input += u.PromptTokens
		l.usage.output += u.CompletionTokens
	} else if u.TotalTokens != 0 {
		l.usage.totalOnly += u.TotalTokens
	}
}
