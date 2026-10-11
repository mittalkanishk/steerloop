package steerloop

import (
	"encoding/json"

	"github.com/cloudwego/eino/schema"
)

// maxAuditRequestJSONBytes caps the serialized request so an audit row stays a
// bounded size. Oldest messages are dropped first.
const maxAuditRequestJSONBytes = 256 * 1024

type auditMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
}

type auditRequestWrapper struct {
	Messages  []auditMessage `json:"messages"`
	Truncated bool           `json:"truncated,omitempty"`
}

// SerializeEinoMessagesForAudit JSON-encodes the messages sent to the model as
// {"messages":[...]}. When the result would exceed 256 KiB the oldest messages
// are dropped and "truncated" is set. It returns the JSON, the number of
// messages kept and the character count of all messages.
func SerializeEinoMessagesForAudit(msgs []*schema.Message) (raw []byte, msgCount, charCount int32) {
	out := make([]auditMessage, 0, len(msgs))
	totalChars := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		am := auditMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			ToolName:   m.ToolName,
		}
		if len(m.ToolCalls) > 0 {
			if b, err := json.Marshal(m.ToolCalls); err == nil {
				am.ToolCalls = b
			}
		}
		totalChars += len(am.Content) + len(am.ToolCalls)
		out = append(out, am)
	}
	wrapper := auditRequestWrapper{Messages: out}
	b, err := json.Marshal(wrapper)
	if err != nil {
		return []byte(`{"messages":[]}`), int32(len(out)), 0
	}
	if len(b) > maxAuditRequestJSONBytes {
		wrapper.Truncated = true
		// Keep head messages until under cap.
		for len(out) > 1 {
			out = out[1:]
			wrapper.Messages = out
			b, err = json.Marshal(wrapper)
			if err != nil || len(b) <= maxAuditRequestJSONBytes {
				break
			}
		}
		if err != nil || len(b) > maxAuditRequestJSONBytes {
			b = []byte(`{"messages":[],"truncated":true}`)
		}
	}
	return b, int32(len(out)), int32(totalChars)
}

// SerializeAssistantToolCalls JSON-encodes the tool calls of an assistant
// message, or "[]" when there are none.
func SerializeAssistantToolCalls(msg *schema.Message) []byte {
	if msg == nil || len(msg.ToolCalls) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(msg.ToolCalls)
	if err != nil {
		return []byte("[]")
	}
	return b
}

func responseKindForMessage(msg *schema.Message) string {
	if msg == nil {
		return "error"
	}
	if len(msg.ToolCalls) > 0 {
		return "tool_calls"
	}
	return "text"
}
