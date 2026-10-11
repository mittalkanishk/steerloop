package steerlooptest

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseScript(t *testing.T) {
	steps, err := ParseScript([]byte(`{"steps":[
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
	assert.Equal(t, []Chunk{{Content: "a"}, {Content: "b", Delay: 7 * time.Millisecond}}, steps[3].Chunks)
}

func TestParseScript_Errors(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"not json":       {`{`, "parse script"},
		"unknown field":  {`{"steps":[{"contnet":"typo"}]}`, "unknown field"},
		"no steps":       {`{"steps":[]}`, "no steps"},
		"unnamed tool":   {`{"steps":[{"tool_calls":[{"id":"x"}]}]}`, "name is required"},
		"bad arg string": {`{"steps":[{"tool_calls":[{"name":"x","arguments":"{oops"}]}]}`, "not valid JSON"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseScript([]byte(tc.in))
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestLoadScript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"steps":[{"content":"hi"}]}`), 0o600))
	steps, err := LoadScript(path)
	require.NoError(t, err)
	assert.Equal(t, "hi", steps[0].Content)

	_, err = LoadScript(filepath.Join(t.TempDir(), "missing.json"))
	assert.Error(t, err)

	require.NoError(t, os.WriteFile(path, []byte(`{"steps":[]}`), 0o600))
	_, err = LoadScript(path)
	assert.ErrorContains(t, err, "s.json")
}
