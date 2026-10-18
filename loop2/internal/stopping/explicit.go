package stopping

import "github.com/cloudwego/eino/schema"

// Explicit runs until a stop signal, the iteration cap or a timeout.
type Explicit struct{}

func (Explicit) Label() string { return "explicit" }

func (Explicit) FinishCall(*schema.Message) (schema.ToolCall, bool) {
	return schema.ToolCall{}, false
}

func (Explicit) AfterText() (ends bool, nudge string) { return false, "" }
