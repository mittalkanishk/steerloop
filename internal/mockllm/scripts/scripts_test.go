package scripts_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/mittalkanishk/steerloop/internal/mockllm/scripts"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func userMessage(text string) []*schema.Message {
	return []*schema.Message{{Role: schema.User, Content: text}}
}

func says(text string) scripts.Step { return scripts.Step{Content: text} }

func fails(err error) scripts.Step { return scripts.Step{Err: err} }

// =============================================================================
// ScriptedModel
// =============================================================================

func TestScriptedModel_PlaysTheStepsThenRepeatsTheLast(t *testing.T) {
	m := scripts.NewScriptedModel(says("one"), fails(errors.New("boom")), says("three"))
	ctx := context.Background()

	msg, err := m.Generate(ctx, userMessage("a"), model.WithTemperature(0.5))
	require.NoError(t, err)
	assert.Equal(t, "one", msg.Content)
	assert.Equal(t, schema.Assistant, msg.Role)

	_, err = m.Generate(ctx, userMessage("b"))
	assert.EqualError(t, err, "boom")

	for range 2 {
		msg, err = m.Generate(ctx, userMessage("c"))
		require.NoError(t, err)
		assert.Equal(t, "three", msg.Content)
	}
	assert.Equal(t, 4, m.CallCount())
	assert.Len(t, m.Calls(), 4)
	assert.Equal(t, "c", m.LastMessages()[0].Content)
}

func TestScriptedModel_WithoutStepsRejectsEveryCall(t *testing.T) {
	m := scripts.NewScriptedModel()
	assert.Nil(t, m.LastMessages())
	assert.Nil(t, m.LastOptions())

	_, err := m.Generate(context.Background(), userMessage("a"))
	assert.ErrorContains(t, err, "no steps")
	_, err = m.Stream(context.Background(), userMessage("a"))
	assert.ErrorContains(t, err, "no steps")
}

func TestScriptedModel_ADelayHonoursTheContext(t *testing.T) {
	m := scripts.NewScriptedModel(scripts.Step{Content: "late", Delay: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := m.Generate(ctx, userMessage("a"))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = m.Stream(ctx, userMessage("a"))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestScriptedModel_StreamsChunksAndJoinsThemForGenerate(t *testing.T) {
	usage := &schema.TokenUsage{PromptTokens: 3, CompletionTokens: 2}
	step := scripts.Step{
		Chunks:    []scripts.Chunk{{Content: "ab"}, {Content: "cd", Delay: 5 * time.Millisecond}},
		ToolCalls: []schema.ToolCall{scripts.ToolCall("c1", "lookup", `{}`)},
		Usage:     usage,
	}

	reader, err := scripts.NewScriptedModel(step).Stream(context.Background(), userMessage("a"))
	require.NoError(t, err)
	defer reader.Close()
	var chunks []*schema.Message
	for {
		chunk, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		chunks = append(chunks, chunk)
	}
	require.Len(t, chunks, 2)
	assert.Empty(t, chunks[0].ToolCalls)
	assert.Equal(t, "lookup", chunks[1].ToolCalls[0].Function.Name, "tool calls ride on the last chunk")
	assert.Equal(t, usage, chunks[1].ResponseMeta.Usage)

	msg, err := scripts.NewScriptedModel(step).Generate(context.Background(), userMessage("a"))
	require.NoError(t, err)
	assert.Equal(t, "abcd", msg.Content, "Generate joins the chunks")
	assert.Equal(t, usage, msg.ResponseMeta.Usage)
}

func TestScriptedModel_AStreamStopsWhenTheContextIsCancelled(t *testing.T) {
	m := scripts.NewScriptedModel(scripts.Step{
		Chunks: []scripts.Chunk{{Content: "a"}, {Content: "b", Delay: time.Second}},
	})
	ctx, cancel := context.WithCancel(context.Background())
	reader, err := m.Stream(ctx, userMessage("a"))
	require.NoError(t, err)
	defer reader.Close()

	_, err = reader.Recv()
	require.NoError(t, err)
	cancel()
	_, err = reader.Recv()
	assert.ErrorIs(t, err, context.Canceled)
}

func TestScriptedModel_AStreamReportsAStepErrorAndSendsTextAsOneChunk(t *testing.T) {
	m := scripts.NewScriptedModel(fails(errors.New("nope")), says("whole"))

	_, err := m.Stream(context.Background(), userMessage("a"))
	assert.EqualError(t, err, "nope")

	reader, err := m.Stream(context.Background(), userMessage("a"))
	require.NoError(t, err)
	msg, err := reader.Recv()
	require.NoError(t, err)
	assert.Equal(t, "whole", msg.Content)
}

func TestScriptedModel_AHandlerReplacesTheScript(t *testing.T) {
	m := &scripts.ScriptedModel{
		Handler: func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
			if msgs[0].Content == "fail" {
				return nil, errors.New("handler failed")
			}
			return &schema.Message{Role: schema.Assistant, Content: "echo " + msgs[0].Content}, nil
		},
	}

	msg, err := m.Generate(context.Background(), userMessage("hi"))
	require.NoError(t, err)
	assert.Equal(t, "echo hi", msg.Content)

	reader, err := m.Stream(context.Background(), userMessage("yo"))
	require.NoError(t, err)
	chunk, err := reader.Recv()
	require.NoError(t, err)
	assert.Equal(t, "echo yo", chunk.Content)

	_, err = m.Stream(context.Background(), userMessage("fail"))
	assert.EqualError(t, err, "handler failed")
	assert.Equal(t, 3, m.CallCount())
}

func TestScriptedModel_RecordsOptionsAndBoundTools(t *testing.T) {
	m := scripts.NewScriptedModel(says("x"))
	bound, err := m.WithTools([]*schema.ToolInfo{{Name: "lookup"}})
	require.NoError(t, err)
	assert.Same(t, m, bound)
	require.Len(t, m.BoundTools(), 1)
	assert.Equal(t, "lookup", m.BoundTools()[0][0].Name)

	_, err = m.Generate(context.Background(), userMessage("a"), model.WithTemperature(0.25), model.WithMaxTokens(9))
	require.NoError(t, err)
	assert.InDelta(t, 0.25, *m.LastOptions().Temperature, 0.0001)
	assert.Equal(t, 9, *m.LastOptions().MaxTokens)
}

// =============================================================================
// Scripts
// =============================================================================

func TestScript_ParsesStepsFromJSON(t *testing.T) {
	steps, err := scripts.ParseScript([]byte(`{"steps":[
		{"tool_calls":[{"id":"c1","name":"lookup","arguments":{"q": "x"}},{"name":"ping"},{"name":"raw","arguments":"{\"a\":1}"}]},
		{"content":"answer","delay_ms":15,"usage":{"input":10,"output":4}},
		{"error":"rate limited"},
		{"chunks":[{"content":"a"},{"content":"b","delay_ms":7}]}
	]}`))
	require.NoError(t, err)
	require.Len(t, steps, 4)

	calls := steps[0].ToolCalls
	require.Len(t, calls, 3)
	assert.Equal(t, "c1", calls[0].ID)
	assert.Equal(t, `{"q":"x"}`, calls[0].Function.Arguments)
	assert.Equal(t, "call_1_2", calls[1].ID, "a missing id is generated")
	assert.Equal(t, "{}", calls[1].Function.Arguments)
	assert.Equal(t, `{"a":1}`, calls[2].Function.Arguments)

	assert.Equal(t, "answer", steps[1].Content)
	assert.Equal(t, 15*time.Millisecond, steps[1].Delay)
	assert.Equal(t, 14, steps[1].Usage.TotalTokens)
	assert.EqualError(t, steps[2].Err, "rate limited")
	assert.Equal(t, []scripts.Chunk{{Content: "a"}, {Content: "b", Delay: 7 * time.Millisecond}}, steps[3].Chunks)
}

func TestScript_RejectsMalformedInput(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"not json", `{`, "parse script"},
		{"unknown field", `{"steps":[{"contnet":"typo"}]}`, "unknown field"},
		{"no steps", `{"steps":[]}`, "no steps"},
		{"unnamed tool", `{"steps":[{"tool_calls":[{"id":"x"}]}]}`, "name is required"},
		{"bad arg string", `{"steps":[{"tool_calls":[{"name":"x","arguments":"{oops"}]}]}`, "not valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scripts.ParseScript([]byte(tc.in))
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestScript_LoadsFromAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"steps":[{"content":"hi"}]}`), 0o600))
	steps, err := scripts.LoadScript(path)
	require.NoError(t, err)
	assert.Equal(t, "hi", steps[0].Content)

	_, err = scripts.LoadScript(filepath.Join(t.TempDir(), "missing.json"))
	assert.Error(t, err)

	require.NoError(t, os.WriteFile(path, []byte(`{"steps":[]}`), 0o600))
	_, err = scripts.LoadScript(path)
	assert.ErrorContains(t, err, "s.json", "the error names the file")
}
