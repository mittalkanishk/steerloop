package audit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mittalkanishk/steerloop/internal/mockllm/scripts"
)

// auditRecord is the shape of the JSON written for a model request.
type auditRecord struct {
	Messages []struct {
		Role       string          `json:"role"`
		Content    string          `json:"content"`
		ToolCalls  json.RawMessage `json:"tool_calls"`
		ToolCallID string          `json:"tool_call_id"`
		ToolName   string          `json:"tool_name"`
	} `json:"messages"`
	Truncated bool `json:"truncated"`
}

func TestAudit_SerializesTheRequestMessages(t *testing.T) {
	raw, count, chars := SerializeMessages([]*schema.Message{
		{Role: schema.System, Content: "sys"},
		nil,
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{scripts.ToolCall("c1", "lookup", `{"q":1}`)}},
		{Role: schema.Tool, Content: "found", ToolCallID: "c1", ToolName: "lookup"},
	})

	var record auditRecord
	require.NoError(t, json.Unmarshal(raw, &record))
	assert.False(t, record.Truncated)
	require.Len(t, record.Messages, 3, "nil messages are skipped")
	assert.EqualValues(t, 3, count)
	assert.Equal(t, "system", record.Messages[0].Role)
	assert.Contains(t, string(record.Messages[1].ToolCalls), `"lookup"`)
	assert.Equal(t, "c1", record.Messages[2].ToolCallID)
	assert.Equal(t, "lookup", record.Messages[2].ToolName)
	assert.EqualValues(t, len("sys")+len(record.Messages[1].ToolCalls)+len("found"), chars)
}

func TestAudit_AnEmptyRequestIsAnEmptyList(t *testing.T) {
	raw, count, chars := SerializeMessages(nil)
	assert.JSONEq(t, `{"messages":[]}`, string(raw))
	assert.Zero(t, count)
	assert.Zero(t, chars)
}

func TestAudit_DropsTheOldestMessagesFirstWhenOverTheCap(t *testing.T) {
	big := strings.Repeat("x", 100*1024)
	raw, count, _ := SerializeMessages([]*schema.Message{
		{Role: schema.User, Content: "first " + big},
		{Role: schema.User, Content: "second " + big},
		{Role: schema.User, Content: "third " + big},
	})

	assert.LessOrEqual(t, len(raw), maxRequestJSONBytes)
	var record auditRecord
	require.NoError(t, json.Unmarshal(raw, &record))
	assert.True(t, record.Truncated)
	require.Len(t, record.Messages, 2)
	assert.EqualValues(t, 2, count)
	assert.True(t, strings.HasPrefix(record.Messages[0].Content, "second "), "the oldest message is dropped first")
}

func TestAudit_DropsASingleOversizedMessage(t *testing.T) {
	raw, _, _ := SerializeMessages([]*schema.Message{
		{Role: schema.User, Content: strings.Repeat("x", 300*1024)},
	})
	assert.JSONEq(t, `{"messages":[],"truncated":true}`, string(raw))
}

func TestAudit_SerializesTheAssistantToolCalls(t *testing.T) {
	assert.Equal(t, "[]", string(SerializeToolCalls(nil)))
	assert.Equal(t, "[]", string(SerializeToolCalls(&schema.Message{})))

	raw := SerializeToolCalls(&schema.Message{ToolCalls: []schema.ToolCall{scripts.ToolCall("c1", "lookup", `{}`)}})
	assert.Contains(t, string(raw), `"id":"c1"`)
}

func TestAudit_ClassifiesTheReply(t *testing.T) {
	assert.Equal(t, "error", responseKind(nil))
	assert.Equal(t, "text", responseKind(&schema.Message{Content: "x"}))
	assert.Equal(t, "tool_calls", responseKind(&schema.Message{
		ToolCalls: []schema.ToolCall{scripts.ToolCall("call_1", "get_profile", `{}`)},
	}))
}

func FuzzSerializeMessages(f *testing.F) {
	f.Add("user", "hello", "call", "tool", `{"a":1}`, 3)
	f.Add("", "", "", "", "", 0)
	f.Fuzz(func(t *testing.T, role, content, callID, toolName, args string, repeat int) {
		if repeat < 0 || repeat > 64 {
			return
		}
		msgs := []*schema.Message{nil}
		for range repeat + 1 {
			msgs = append(msgs, &schema.Message{
				Role:       schema.RoleType(role),
				Content:    content,
				ToolCallID: callID,
				ToolName:   toolName,
				ToolCalls:  []schema.ToolCall{scripts.ToolCall(callID, toolName, args)},
			})
		}
		raw, _, _ := SerializeMessages(msgs)
		if !json.Valid(raw) {
			t.Fatalf("invalid JSON: %q", raw)
		}
		if len(raw) > maxRequestJSONBytes {
			t.Fatalf("output %d bytes exceeds the cap", len(raw))
		}
		if !json.Valid(SerializeToolCalls(msgs[1])) {
			t.Fatal("invalid tool call JSON")
		}
	})
}
