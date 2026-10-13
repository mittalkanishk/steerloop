package steerloop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type auditOut struct {
	Messages []struct {
		Role       string          `json:"role"`
		Content    string          `json:"content"`
		ToolCalls  json.RawMessage `json:"tool_calls"`
		ToolCallID string          `json:"tool_call_id"`
		ToolName   string          `json:"tool_name"`
	} `json:"messages"`
	Truncated bool `json:"truncated"`
}

func TestAudit_SerializesMessages(t *testing.T) {
	msgs := []*schema.Message{
		{Role: schema.System, Content: "sys"},
		nil,
		{Role: schema.Assistant, ToolCalls: []schema.ToolCall{toolCall("c1", "lookup", `{"q":1}`)}},
		{Role: schema.Tool, Content: "found", ToolCallID: "c1", ToolName: "lookup"},
	}
	raw, count, chars := SerializeEinoMessagesForAudit(msgs)

	var out auditOut
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.False(t, out.Truncated)
	require.Len(t, out.Messages, 3, "nil messages are skipped")
	assert.EqualValues(t, 3, count)
	assert.Equal(t, "system", out.Messages[0].Role)
	assert.Contains(t, string(out.Messages[1].ToolCalls), `"lookup"`)
	assert.Equal(t, "c1", out.Messages[2].ToolCallID)
	assert.Equal(t, "lookup", out.Messages[2].ToolName)
	assert.EqualValues(t, len("sys")+len(out.Messages[1].ToolCalls)+len("found"), chars)
}

func TestAudit_EmptyInput(t *testing.T) {
	raw, count, chars := SerializeEinoMessagesForAudit(nil)
	assert.JSONEq(t, `{"messages":[]}`, string(raw))
	assert.Zero(t, count)
	assert.Zero(t, chars)
}

func TestAudit_TruncatesOldestFirst(t *testing.T) {
	big := strings.Repeat("x", 100*1024)
	msgs := []*schema.Message{
		{Role: schema.User, Content: "first " + big},
		{Role: schema.User, Content: "second " + big},
		{Role: schema.User, Content: "third " + big},
	}
	raw, count, _ := SerializeEinoMessagesForAudit(msgs)

	assert.LessOrEqual(t, len(raw), maxAuditRequestJSONBytes)
	var out auditOut
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.True(t, out.Truncated)
	require.Len(t, out.Messages, 2)
	assert.EqualValues(t, 2, count)
	assert.True(t, strings.HasPrefix(out.Messages[0].Content, "second "), "the oldest message is dropped first")
}

func TestAudit_SingleOversizedMessage(t *testing.T) {
	raw, _, _ := SerializeEinoMessagesForAudit([]*schema.Message{
		{Role: schema.User, Content: strings.Repeat("x", 300*1024)},
	})
	assert.JSONEq(t, `{"messages":[],"truncated":true}`, string(raw))
}

func TestAudit_AssistantToolCalls(t *testing.T) {
	assert.Equal(t, "[]", string(SerializeAssistantToolCalls(nil)))
	assert.Equal(t, "[]", string(SerializeAssistantToolCalls(&schema.Message{})))
	raw := SerializeAssistantToolCalls(&schema.Message{ToolCalls: []schema.ToolCall{toolCall("c1", "lookup", `{}`)}})
	assert.Contains(t, string(raw), `"id":"c1"`)
	assert.Equal(t, "error", responseKindForMessage(nil))
	assert.Equal(t, "text", responseKindForMessage(&schema.Message{Content: "x"}))
}

func TestNewToolInfo(t *testing.T) {
	ti, err := NewToolInfo("ping", "Ping", nil)
	require.NoError(t, err)
	assert.Equal(t, "ping", ti.Name)
	assert.Nil(t, ti.ParamsOneOf)

	ti, err = NewToolInfo("search", "Search", map[string]any{
		"type":       "object",
		"properties": map[string]any{"q": map[string]any{"type": "string"}},
		"required":   []string{"q"},
	})
	require.NoError(t, err)
	js, err := ti.ToJSONSchema()
	require.NoError(t, err)
	assert.Equal(t, []string{"q"}, js.Required)

	_, err = NewToolInfo("bad", "", map[string]any{"type": make(chan int)})
	assert.ErrorContains(t, err, "marshal params")
	_, err = NewToolInfo("bad", "", map[string]any{"properties": "not an object"})
	assert.ErrorContains(t, err, "parse params")
}

func FuzzNewToolInfo(f *testing.F) {
	f.Add("search", `{"type":"object","properties":{"q":{"type":"string"}}}`)
	f.Add("", `{}`)
	f.Add("x", `{"properties":7}`)
	f.Fuzz(func(t *testing.T, name, schemaJSON string) {
		var params map[string]any
		if err := json.Unmarshal([]byte(schemaJSON), &params); err != nil {
			return
		}
		_, _ = NewToolInfo(name, "desc", params) // must not panic
	})
}

func FuzzSerializeEinoMessagesForAudit(f *testing.F) {
	f.Add("user", "hello", "call", "tool", `{"a":1}`, 3)
	f.Add("", "", "", "", "", 0)
	f.Fuzz(func(t *testing.T, role, content, callID, toolName, args string, repeat int) {
		if repeat < 0 || repeat > 64 {
			return
		}
		msgs := []*schema.Message{nil}
		for i := 0; i <= repeat; i++ {
			msgs = append(msgs, &schema.Message{
				Role:       schema.RoleType(role),
				Content:    content,
				ToolCallID: callID,
				ToolName:   toolName,
				ToolCalls:  []schema.ToolCall{toolCall(callID, toolName, args)},
			})
		}
		raw, _, _ := SerializeEinoMessagesForAudit(msgs)
		if !json.Valid(raw) {
			t.Fatalf("invalid JSON: %q", raw)
		}
		if len(raw) > maxAuditRequestJSONBytes {
			t.Fatalf("output %d bytes exceeds the cap", len(raw))
		}
		if !json.Valid(SerializeAssistantToolCalls(msgs[1])) {
			t.Fatal("invalid tool call JSON")
		}
	})
}
