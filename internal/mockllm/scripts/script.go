package scripts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cloudwego/eino/schema"
)

// scriptFile is the JSON form of a script:
//
//	{"steps": [
//	  {"tool_calls": [{"id": "c1", "name": "lookup", "arguments": {"q": "x"}}]},
//	  {"content": "The answer.", "usage": {"input": 120, "output": 15}},
//	  {"error": "rate limited"},
//	  {"chunks": [{"content": "Hel"}, {"content": "lo", "delay_ms": 20}]}
//	]}
type scriptFile struct {
	Steps []scriptStep `json:"steps"`
}

type scriptStep struct {
	Content   string           `json:"content"`
	ToolCalls []scriptToolCall `json:"tool_calls"`
	Error     string           `json:"error"`
	DelayMs   int              `json:"delay_ms"`
	Chunks    []scriptChunk    `json:"chunks"`
	Usage     *scriptUsage     `json:"usage"`
}

type scriptToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type scriptChunk struct {
	Content string `json:"content"`
	DelayMs int    `json:"delay_ms"`
}

type scriptUsage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// LoadScript reads a JSON script file into steps for NewScriptedModel.
func LoadScript(path string) ([]Step, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	steps, err := ParseScript(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return steps, nil
}

// ParseScript decodes a JSON script. Tool call arguments may be a JSON object
// or a string holding JSON. Unknown fields are rejected so a typo is not
// silently ignored.
func ParseScript(data []byte) ([]Step, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var file scriptFile
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse script: %w", err)
	}
	if len(file.Steps) == 0 {
		return nil, errors.New("script has no steps")
	}

	steps := make([]Step, 0, len(file.Steps))
	for i, s := range file.Steps {
		step := Step{
			Content: s.Content,
			Delay:   time.Duration(s.DelayMs) * time.Millisecond,
		}
		if s.Error != "" {
			step.Err = errors.New(s.Error)
		}
		for _, c := range s.Chunks {
			step.Chunks = append(step.Chunks, Chunk{Content: c.Content, Delay: time.Duration(c.DelayMs) * time.Millisecond})
		}
		if s.Usage != nil {
			step.Usage = &schema.TokenUsage{
				PromptTokens:     s.Usage.Input,
				CompletionTokens: s.Usage.Output,
				TotalTokens:      s.Usage.Input + s.Usage.Output,
			}
		}
		for j, tc := range s.ToolCalls {
			if tc.Name == "" {
				return nil, fmt.Errorf("step %d tool call %d: name is required", i+1, j+1)
			}
			args, err := scriptArguments(tc.Arguments)
			if err != nil {
				return nil, fmt.Errorf("step %d tool call %d: %w", i+1, j+1, err)
			}
			id := tc.ID
			if id == "" {
				id = fmt.Sprintf("call_%d_%d", i+1, j+1)
			}
			step.ToolCalls = append(step.ToolCalls, ToolCall(id, tc.Name, args))
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func scriptArguments(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "{}", nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if !json.Valid([]byte(asString)) {
			return "", errors.New("arguments string is not valid JSON")
		}
		return asString, nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", fmt.Errorf("arguments: %w", err)
	}
	return buf.String(), nil
}
