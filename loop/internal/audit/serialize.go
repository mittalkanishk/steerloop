package audit

import (
	"encoding/json"

	"github.com/cloudwego/eino/schema"
)

// maxRequestJSONBytes caps the serialized request so an audit row stays a
// bounded size. Oldest messages are dropped first.
const maxRequestJSONBytes = 256 * 1024

type message struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
}

type request struct {
	Messages  []message `json:"messages"`
	Truncated bool      `json:"truncated,omitempty"`
}

// SerializeMessages JSON-encodes the messages sent to the model as
// {"messages":[...]}. Over 256 KiB the oldest messages are dropped and
// "truncated" is set. It returns the JSON, the number of messages kept and
// the character count of all messages.
func SerializeMessages(msgs []*schema.Message) (raw []byte, msgCount, charCount int32) {
	out := make([]message, 0, len(msgs))
	totalChars := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		am := message{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID, ToolName: m.ToolName}
		if len(m.ToolCalls) > 0 {
			if b, err := json.Marshal(m.ToolCalls); err == nil {
				am.ToolCalls = b
			}
		}
		totalChars += len(am.Content) + len(am.ToolCalls)
		out = append(out, am)
	}

	req := request{Messages: out}
	b, err := json.Marshal(req)
	if err != nil {
		return []byte(`{"messages":[]}`), int32(len(out)), 0
	}
	if len(b) > maxRequestJSONBytes {
		req.Truncated = true
		for len(out) > 1 {
			out = out[1:]
			req.Messages = out
			b, err = json.Marshal(req)
			if err != nil || len(b) <= maxRequestJSONBytes {
				break
			}
		}
		if err != nil || len(b) > maxRequestJSONBytes {
			b = []byte(`{"messages":[],"truncated":true}`)
		}
	}
	return b, int32(len(out)), int32(totalChars)
}

// SerializeToolCalls JSON-encodes an assistant message's tool calls, or "[]".
func SerializeToolCalls(msg *schema.Message) []byte {
	if msg == nil || len(msg.ToolCalls) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(msg.ToolCalls)
	if err != nil {
		return []byte("[]")
	}
	return b
}

func responseKind(msg *schema.Message) string {
	switch {
	case msg == nil:
		return "error"
	case len(msg.ToolCalls) > 0:
		return "tool_calls"
	default:
		return "text"
	}
}
