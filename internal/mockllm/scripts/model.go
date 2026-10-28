// Package scripts is a chat model that plays a script: replies in order, from
// Go (Step) or a JSON file (LoadScript). It backs the CLI's script:<file>
// model, the examples and the loop tests, so none of them needs a provider.
package scripts

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Step is what the scripted model returns for one call.
type Step struct {
	// Content is the assistant text.
	Content string
	// ToolCalls are the tool calls the assistant makes.
	ToolCalls []schema.ToolCall
	// Err, when set, is returned instead of a message.
	Err error
	// Delay is waited before anything is returned.
	Delay time.Duration
	// Chunks, when set, replaces Content on the streaming path: each chunk is
	// sent after its own delay. ToolCalls ride on the last chunk.
	Chunks []Chunk
	// Usage, when set, is reported as the token usage of the reply.
	Usage *schema.TokenUsage
}

// Chunk is one streamed piece of a Step.
type Chunk struct {
	Content string
	Delay   time.Duration
}

// Handler replaces the script for a call when set on a ScriptedModel.
type Handler func(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error)

// ScriptedModel is an Eino ToolCallingChatModel that plays Steps in order.
// When the script runs out the last step repeats. It records every call.
type ScriptedModel struct {
	mu      sync.Mutex
	steps   []Step
	next    int
	calls   [][]*schema.Message
	options [][]model.Option
	bound   [][]*schema.ToolInfo

	// Handler, when set, answers every call instead of the script.
	Handler Handler
}

// NewScriptedModel returns a model that plays steps.
func NewScriptedModel(steps ...Step) *ScriptedModel {
	return &ScriptedModel{steps: steps}
}

func (m *ScriptedModel) record(msgs []*schema.Message, opts []model.Option) {
	cp := make([]*schema.Message, len(msgs))
	copy(cp, msgs)
	m.mu.Lock()
	m.calls = append(m.calls, cp)
	m.options = append(m.options, opts)
	m.mu.Unlock()
}

func (m *ScriptedModel) nextStep() (Step, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.steps) == 0 {
		return Step{}, errors.New("scripts: scripted model has no steps")
	}
	idx := m.next
	if idx >= len(m.steps) {
		idx = len(m.steps) - 1
	}
	m.next++
	return m.steps[idx], nil
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s Step) message() *schema.Message {
	content := s.Content
	if content == "" {
		for _, c := range s.Chunks {
			content += c.Content
		}
	}
	msg := &schema.Message{Role: schema.Assistant, Content: content, ToolCalls: s.ToolCalls}
	if s.Usage != nil {
		msg.ResponseMeta = &schema.ResponseMeta{Usage: s.Usage}
	}
	return msg
}

// Generate returns the next step as one message.
func (m *ScriptedModel) Generate(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.record(msgs, opts)
	if m.Handler != nil {
		return m.Handler(ctx, msgs, opts...)
	}
	step, err := m.nextStep()
	if err != nil {
		return nil, err
	}
	if err := wait(ctx, step.Delay); err != nil {
		return nil, err
	}
	if step.Err != nil {
		return nil, step.Err
	}
	return step.message(), nil
}

// Stream returns the next step as a stream. A step with Chunks is sent chunk
// by chunk with its delays; any other step is sent as a single chunk.
func (m *ScriptedModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.Handler != nil {
		msg, err := m.Generate(ctx, msgs, opts...)
		if err != nil {
			return nil, err
		}
		return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
	}

	m.record(msgs, opts)
	step, err := m.nextStep()
	if err != nil {
		return nil, err
	}
	if err := wait(ctx, step.Delay); err != nil {
		return nil, err
	}
	if step.Err != nil {
		return nil, step.Err
	}
	if len(step.Chunks) == 0 {
		return schema.StreamReaderFromArray([]*schema.Message{step.message()}), nil
	}

	sr, sw := schema.Pipe[*schema.Message](len(step.Chunks))
	go func() {
		defer sw.Close()
		for i, c := range step.Chunks {
			if err := wait(ctx, c.Delay); err != nil {
				sw.Send(nil, err)
				return
			}
			msg := &schema.Message{Role: schema.Assistant, Content: c.Content}
			if i == len(step.Chunks)-1 {
				msg.ToolCalls = step.ToolCalls
				if step.Usage != nil {
					msg.ResponseMeta = &schema.ResponseMeta{Usage: step.Usage}
				}
			}
			if sw.Send(msg, nil) {
				return
			}
		}
	}()
	return sr, nil
}

// WithTools records the tools and returns the same model.
func (m *ScriptedModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.mu.Lock()
	m.bound = append(m.bound, tools)
	m.mu.Unlock()
	return m, nil
}

// CallCount returns the number of model calls made.
func (m *ScriptedModel) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// Calls returns the messages of every call, in order.
func (m *ScriptedModel) Calls() [][]*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]*schema.Message, len(m.calls))
	copy(out, m.calls)
	return out
}

// LastMessages returns the messages of the most recent call.
func (m *ScriptedModel) LastMessages() []*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.calls) == 0 {
		return nil
	}
	return m.calls[len(m.calls)-1]
}

// LastOptions returns the options of the most recent call, resolved into
// Eino's common option set.
func (m *ScriptedModel) LastOptions() *model.Options {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.options) == 0 {
		return nil
	}
	return model.GetCommonOptions(nil, m.options[len(m.options)-1]...)
}

// BoundTools returns the tool lists passed to WithTools, in order.
func (m *ScriptedModel) BoundTools() [][]*schema.ToolInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]*schema.ToolInfo, len(m.bound))
	copy(out, m.bound)
	return out
}

// ToolCall builds an Eino tool call.
func ToolCall(id, name, arguments string) schema.ToolCall {
	return schema.ToolCall{
		ID:       id,
		Type:     "function",
		Function: schema.FunctionCall{Name: name, Arguments: arguments},
	}
}
