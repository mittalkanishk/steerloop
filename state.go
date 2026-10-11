package steerloop

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// SendControlSignal queues a control for the loop. It returns false when the
// control queue is full. Use Stop to stop a loop: it cannot be lost.
func (l *Loop) SendControlSignal(ctrl Control) bool {
	select {
	case l.controlCh <- ctrl:
		return true
	default:
		return false
	}
}

// Stop asks the loop to stop. It takes effect at the next iteration boundary,
// or at once if a pause function is blocked waiting for a reply. It does not
// interrupt a model call or a tool that is already running.
func (l *Loop) Stop() {
	l.stopOnce.Do(func() { close(l.stopCh) })
}

// Inject queues a user message. It returns false when the control queue is full.
func (l *Loop) Inject(message string) bool {
	return l.SendControlSignal(NewInjectControl(message))
}

// Wait blocks until the run has ended and the event channel is closed.
func (l *Loop) Wait() {
	<-l.done
}

// Events returns the event channel, the same one Start returns.
func (l *Loop) Events() <-chan Event {
	return l.eventCh
}

// Result returns the outcome of the run. ok is false until the loop has ended.
// It is the way to learn the outcome without reading the event channel.
func (l *Loop) Result() (DoneData, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.result, l.hasResult
}

// GetState returns the lifecycle state.
func (l *Loop) GetState() LoopState {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.state
}

// GetCurrentIteration returns the number of model calls started so far.
func (l *Loop) GetCurrentIteration() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.iteration
}

// GetRunningMessages returns a copy of the conversation as it stands now.
func (l *Loop) GetRunningMessages() []*schema.Message {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]*schema.Message, len(l.runningMessages))
	copy(out, l.runningMessages)
	return out
}

func (l *Loop) stopRequested() bool {
	select {
	case <-l.stopCh:
		return true
	default:
		return false
	}
}

func (l *Loop) nextIteration() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.iteration++
	return l.iteration
}

func (l *Loop) setState(s LoopState) {
	l.mu.Lock()
	l.state = s
	l.mu.Unlock()
}

func (l *Loop) maxIterations() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cfg.MaxIterations
}

func (l *Loop) temperature() float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cfg.Temperature
}

func (l *Loop) reflectionEnabled() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cfg.EnableReflection
}

func (l *Loop) appendRunningMessages(msg *schema.Message) {
	l.mu.Lock()
	l.runningMessages = append(l.runningMessages, msg)
	l.mu.Unlock()
}

func (l *Loop) usageSnapshot() llmUsageAccumulator {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.usage
}

// addUsage adds the token usage reported on an assistant message.
func (l *Loop) addUsage(msg *schema.Message) {
	if msg == nil || msg.ResponseMeta == nil || msg.ResponseMeta.Usage == nil {
		return
	}
	u := msg.ResponseMeta.Usage
	l.mu.Lock()
	defer l.mu.Unlock()
	if u.PromptTokens != 0 || u.CompletionTokens != 0 {
		l.usage.input += u.PromptTokens
		l.usage.output += u.CompletionTokens
	} else if u.TotalTokens != 0 {
		l.usage.totalOnly += u.TotalTokens
	}
}

// logRunningMessages logs the messages added since the last call.
func (l *Loop) logRunningMessages(ctx context.Context, iteration int) {
	l.mu.Lock()
	start := l.logged
	if start > len(l.runningMessages) {
		start = 0
	}
	added := make([]*schema.Message, len(l.runningMessages)-start)
	copy(added, l.runningMessages[start:])
	l.logged = len(l.runningMessages)
	l.mu.Unlock()

	if text := formatTranscript(start, added); text != "" {
		l.logger.Log(ctx, l.cfg.TranscriptLogLevel, "agent loop diff",
			slog.Int("iteration", iteration), slog.String("diff", text))
	}
}

func formatTranscript(start int, msgs []*schema.Message) string {
	lines := make([]string, 0, len(msgs))
	for i, m := range msgs {
		if m == nil {
			continue
		}
		body := m.Content
		if body == "" && len(m.ToolCalls) > 0 {
			names := make([]string, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				names[j] = tc.Function.Name
			}
			body = "tool_calls: " + strings.Join(names, ", ")
		}
		lines = append(lines, fmt.Sprintf("%d %s %s", start+i, m.Role, body))
	}
	return strings.Join(lines, "\n")
}
