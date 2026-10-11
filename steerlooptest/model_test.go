package steerlooptest

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func user(text string) []*schema.Message {
	return []*schema.Message{{Role: schema.User, Content: text}}
}

func TestScriptedModel_PlaysStepsAndRepeatsLast(t *testing.T) {
	m := NewScriptedModel(Step{Content: "one"}, Step{Err: errors.New("boom")}, Step{Content: "three"})
	ctx := context.Background()

	msg, err := m.Generate(ctx, user("a"), model.WithTemperature(0.5))
	require.NoError(t, err)
	assert.Equal(t, "one", msg.Content)
	assert.Equal(t, schema.Assistant, msg.Role)

	_, err = m.Generate(ctx, user("b"))
	assert.EqualError(t, err, "boom")

	for i := 0; i < 2; i++ {
		msg, err = m.Generate(ctx, user("c"))
		require.NoError(t, err)
		assert.Equal(t, "three", msg.Content)
	}
	assert.Equal(t, 4, m.CallCount())
	assert.Len(t, m.Calls(), 4)
	assert.Equal(t, "c", m.LastMessages()[0].Content)
}

func TestScriptedModel_EmptyScript(t *testing.T) {
	m := NewScriptedModel()
	assert.Nil(t, m.LastMessages())
	assert.Nil(t, m.LastOptions())
	_, err := m.Generate(context.Background(), user("a"))
	assert.ErrorContains(t, err, "no steps")
	_, err = m.Stream(context.Background(), user("a"))
	assert.ErrorContains(t, err, "no steps")
}

func TestScriptedModel_DelayHonoursContext(t *testing.T) {
	m := NewScriptedModel(Step{Content: "late", Delay: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := m.Generate(ctx, user("a"))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = m.Stream(ctx, user("a"))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestScriptedModel_StreamChunks(t *testing.T) {
	usage := &schema.TokenUsage{PromptTokens: 3, CompletionTokens: 2}
	m := NewScriptedModel(Step{
		Chunks:    []Chunk{{Content: "ab"}, {Content: "cd", Delay: 5 * time.Millisecond}},
		ToolCalls: []schema.ToolCall{ToolCall("c1", "lookup", `{}`)},
		Usage:     usage,
	})
	sr, err := m.Stream(context.Background(), user("a"))
	require.NoError(t, err)
	defer sr.Close()

	var chunks []*schema.Message
	for {
		c, rerr := sr.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		require.NoError(t, rerr)
		chunks = append(chunks, c)
	}
	require.Len(t, chunks, 2)
	assert.Empty(t, chunks[0].ToolCalls)
	assert.Equal(t, "lookup", chunks[1].ToolCalls[0].Function.Name)
	assert.Equal(t, usage, chunks[1].ResponseMeta.Usage)

	// The same step through Generate joins the chunks.
	m2 := NewScriptedModel(Step{Chunks: []Chunk{{Content: "ab"}, {Content: "cd"}}, Usage: usage})
	msg, err := m2.Generate(context.Background(), user("a"))
	require.NoError(t, err)
	assert.Equal(t, "abcd", msg.Content)
	assert.Equal(t, usage, msg.ResponseMeta.Usage)
}

func TestScriptedModel_StreamCancelledMidway(t *testing.T) {
	m := NewScriptedModel(Step{Chunks: []Chunk{{Content: "a"}, {Content: "b", Delay: time.Second}}})
	ctx, cancel := context.WithCancel(context.Background())
	sr, err := m.Stream(ctx, user("a"))
	require.NoError(t, err)
	defer sr.Close()
	_, err = sr.Recv()
	require.NoError(t, err)
	cancel()
	_, err = sr.Recv()
	assert.ErrorIs(t, err, context.Canceled)
}

func TestScriptedModel_StreamErrorAndSingleChunk(t *testing.T) {
	m := NewScriptedModel(Step{Err: errors.New("nope")}, Step{Content: "whole"})
	_, err := m.Stream(context.Background(), user("a"))
	assert.EqualError(t, err, "nope")
	sr, err := m.Stream(context.Background(), user("a"))
	require.NoError(t, err)
	msg, err := sr.Recv()
	require.NoError(t, err)
	assert.Equal(t, "whole", msg.Content)
}

func TestScriptedModel_Handler(t *testing.T) {
	m := &ScriptedModel{Handler: func(_ context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
		if msgs[0].Content == "fail" {
			return nil, errors.New("handler failed")
		}
		return &schema.Message{Role: schema.Assistant, Content: "echo " + msgs[0].Content}, nil
	}}
	msg, err := m.Generate(context.Background(), user("hi"))
	require.NoError(t, err)
	assert.Equal(t, "echo hi", msg.Content)

	sr, err := m.Stream(context.Background(), user("yo"))
	require.NoError(t, err)
	chunk, err := sr.Recv()
	require.NoError(t, err)
	assert.Equal(t, "echo yo", chunk.Content)

	_, err = m.Stream(context.Background(), user("fail"))
	assert.EqualError(t, err, "handler failed")
	assert.Equal(t, 3, m.CallCount())
}

func TestScriptedModel_OptionsAndTools(t *testing.T) {
	m := NewScriptedModel(Step{Content: "x"})
	bound, err := m.WithTools([]*schema.ToolInfo{{Name: "lookup"}})
	require.NoError(t, err)
	assert.Same(t, m, bound)
	require.Len(t, m.BoundTools(), 1)
	assert.Equal(t, "lookup", m.BoundTools()[0][0].Name)

	_, err = m.Generate(context.Background(), user("a"), model.WithTemperature(0.25), model.WithMaxTokens(9))
	require.NoError(t, err)
	assert.InDelta(t, 0.25, *m.LastOptions().Temperature, 0.0001)
	assert.Equal(t, 9, *m.LastOptions().MaxTokens)
}

func TestRecordingExecutor(t *testing.T) {
	e := NewRecordingExecutor(nil)
	out, err := e.ExecuteTool(context.Background(), "lookup", `{"q":1}`)
	require.NoError(t, err)
	assert.Equal(t, "result from lookup", out)

	e2 := NewRecordingExecutor(func(_ context.Context, name, args string) (string, error) {
		return "", errors.New(name + " failed with " + args)
	})
	_, err = e2.ExecuteTool(context.Background(), "a", "{}")
	assert.EqualError(t, err, "a failed with {}")
	_, _ = e2.ExecuteTool(context.Background(), "b", "[]")
	assert.Equal(t, 2, e2.CallCount())
	assert.Equal(t, []string{"a", "b"}, e2.CalledTools())
	assert.Equal(t, []ExecutedCall{{Name: "a", Args: "{}"}, {Name: "b", Args: "[]"}}, e2.Calls())
}
