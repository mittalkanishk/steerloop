package stopping_policy

import "github.com/cloudwego/eino/schema"

// ExternalFinish runs until a stop signal, the iteration cap or a timeout.
type ExternalFinish struct{}

func (ExternalFinish) Label() string { return "external_finish" }

func (ExternalFinish) Instructions() string {
	return "This run does not end when you finish a task. Only the user, or a step or " +
		"time limit, can end it. Work step by step. When you have nothing left to do, " +
		"reply with a short status of what is done and what you are waiting for, " +
		"rather than repeating earlier work."
}

func (ExternalFinish) FinishCall(*schema.Message) (schema.ToolCall, bool) {
	return schema.ToolCall{}, false
}

func (ExternalFinish) AfterText() (ends bool, nudge string) {
	return false, ""
}
