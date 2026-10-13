package steerloop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Loop runs one agent conversation: call the model, execute the tools it asks
// for, feed the results back, repeat. A Loop is started once and is safe for
// concurrent use.
type Loop struct {
	// cfg is fixed after New except for MaxIterations, Temperature and
	// EnableReflection, which ModifyConfig changes under mu.
	cfg LoopConfig

	llm       model.ToolCallingChatModel
	tools     []*schema.ToolInfo
	executor  ToolExecutor
	logger    *slog.Logger
	controlCh chan Control
	stopCh    chan struct{}
	stopOnce  sync.Once

	// evMu orders sends on eventCh against its close.
	evMu             sync.RWMutex
	eventCh          chan Event
	eventClosed      bool
	dropped          atomic.Int64
	mu               sync.RWMutex
	state            LoopState
	iteration        int
	startingMessages []*schema.Message
	runningMessages  []*schema.Message
	logged           int // messages already written to the transcript log
	usage            llmUsageAccumulator
	lastError        string
	result           DoneData
	hasResult        bool
	doneOnce         sync.Once
	done             chan struct{}
}

// New creates a Loop. llm is any Eino tool-calling chat model; when tools is
// not empty the model is bound to them with WithTools. executor runs the
// tools. A nil logger discards log output.
func New(cfg LoopConfig, llm model.ToolCallingChatModel, tools []*schema.ToolInfo, executor ToolExecutor, logger *slog.Logger) (*Loop, error) {
	if executor == nil {
		return nil, errors.New("steerloop: tool executor not configured")
	}
	if llm == nil {
		return nil, errors.New("steerloop: LLM not configured")
	}
	if logger == nil {
		logger = slog.New(discardHandler{})
	}

	if len(tools) > 0 {
		bound, err := llm.WithTools(tools)
		if err != nil {
			return nil, fmt.Errorf("steerloop: WithTools: %w", err)
		}
		if bound == nil {
			return nil, errors.New("steerloop: WithTools returned no model")
		}
		llm = bound
	}

	buffer := cfg.EventBuffer

	if buffer <= 0 {
		buffer = defaultEventBuffer
	}

	return &Loop{
		cfg:       cfg,
		llm:       llm,
		tools:     tools,
		executor:  executor,
		logger:    logger,
		controlCh: make(chan Control, controlChBuffer),
		stopCh:    make(chan struct{}),
		eventCh:   make(chan Event, buffer),
		state:     StateIdle,
		done:      make(chan struct{}),
	}, nil
}

// Start begins the run with the given messages and returns the event channel,
// which is closed after the done event. The loop keeps its own copy of the
// slice, so the caller's slice is never appended to. A second call returns a
// channel holding one fatal error.
func (l *Loop) Start(ctx context.Context, startingMessages []*schema.Message) <-chan Event {

	l.mu.Lock()

	if l.state != StateIdle {
		l.mu.Unlock()
		return failedStart("loop already started")
	}

	l.state = StateRunning

	l.startingMessages = make([]*schema.Message, len(startingMessages))
	copy(l.startingMessages, startingMessages)

	l.runningMessages = make([]*schema.Message, len(startingMessages))
	copy(l.runningMessages, startingMessages)

	seed := make([]*schema.Message, len(startingMessages))
	copy(seed, startingMessages)

	l.logged = len(l.runningMessages)
	l.mu.Unlock()

	if text := formatTranscript(0, seed); text != "" {
		l.logger.Log(ctx, l.cfg.TranscriptLogLevel, "agent loop seed", slog.String("seed", text))
	}

	go l.run(ctx)

	return l.eventCh
}

func failedStart(msg string) <-chan Event {

	errCh := make(chan Event, 1)

	errCh <- Event{
		Type:      EventError,
		Data:      ErrorData{Error: msg, Fatal: true},
		Timestamp: time.Now().UTC(),
	}

	close(errCh)

	return errCh
}

func (l *Loop) run(ctx context.Context) {
	defer l.finishRun()

	var totalCtx context.Context
	var totalCancel context.CancelFunc

	if l.cfg.TotalTimeout > 0 {
		totalCtx, totalCancel = context.WithTimeout(ctx, l.cfg.TotalTimeout)
	} else {
		totalCtx, totalCancel = context.WithCancel(ctx)
	}
	defer totalCancel()

	for {
		maxIter := l.maxIterations()

		if l.GetCurrentIteration() >= maxIter {
			l.emitDone(DoneReasonMaxIterations, "")
			return
		}
		if totalCtx.Err() != nil {
			l.emitDone(DoneReasonTimeout, "")
			return
		}
		if l.drainControls() || l.stopRequested() {
			l.finishStopped()
			return
		}

		currentIter := l.nextIteration()
		l.logger.Debug("agent loop iteration", slog.Int("iteration", currentIter), slog.Int("max", maxIter))
		l.emitEvent(EventThinking, ThinkingData{
			Message: fmt.Sprintf("Reasoning (step %d/%d)…", currentIter, maxIter),
		})

		messagesFedToLLM := l.GetRunningMessages()
		if l.cfg.HistoryCompactor != nil {
			messagesFedToLLM = l.cfg.HistoryCompactor(messagesFedToLLM)
		}

		usageBefore := l.usageSnapshot()
		callStart := time.Now()
		reply, err := l.callModel(totalCtx, l.llm, messagesFedToLLM)
		llmDurationMs := time.Since(callStart).Milliseconds()

		if err != nil {
			l.fail(err.Error())
			return
		}
		if reply == nil {
			l.fail("empty response from LLM")
			return
		}
		l.addUsage(reply)

		if len(reply.ToolCalls) > 0 {
			if l.cfg.StopCondition == StopOnFinishTool {
				for _, tc := range reply.ToolCalls {
					if tc.Function.Name == FinishToolName {
						l.emitEvent(EventToolCall, ToolCallData{
							ToolCallID: tc.ID,
							Name:       FinishToolName,
							Arguments:  tc.Function.Arguments,
						})
						l.emitDone(DoneReasonFinished, tc.Function.Arguments)
						return
					}
				}
			}

			l.appendRunningMessages(reply)
			l.emitLLMTurnEvent(currentIter, usageBefore, messagesFedToLLM, reply, llmDurationMs)

			ended := false
			for _, tc := range reply.ToolCalls {
				if pause, ok := l.cfg.Pauses[tc.Function.Name]; ok && pause != nil {
					if !l.runPause(totalCtx, pause, tc) {
						ended = true
						break
					}
					continue
				}
				l.runTool(totalCtx, tc)
			}
			l.logRunningMessages(totalCtx, currentIter)
			if ended {
				return
			}
			continue
		}

		content := reply.Content
		l.emitEvent(EventResponse, ResponseData{Content: content})
		l.appendRunningMessages(reply)

		switch l.cfg.StopCondition {
		case StopOnNoToolCalls:
			if l.reflectionEnabled() {
				l.doReflection(totalCtx, content)
			}
			l.logRunningMessages(totalCtx, currentIter)
			l.emitDone(DoneReasonFinished, content)
			return

		case StopOnFinishTool:
			// A text-only reply here is the model narrating instead of acting.
			// Nudge it back to a tool call. Asking a person is done with a
			// pause tool, never inferred from the shape of the text.
			if !l.cfg.DisableAutoNudge {
				nudge := l.cfg.AutoNudgeText
				if nudge == "" {
					nudge = defaultAutoNudge
				}
				l.appendRunningMessages(&schema.Message{Role: schema.User, Content: nudge})
				l.logger.Debug("agent loop auto-nudged after text-only response",
					slog.Int("iteration", currentIter))
			}
			l.logRunningMessages(totalCtx, currentIter)

		case StopOnExplicit:
			l.logRunningMessages(totalCtx, currentIter)
		}
	}
}

// runTool executes one tool call and records its result in the history.
func (l *Loop) runTool(ctx context.Context, tc schema.ToolCall) {
	l.emitEvent(EventToolCall, ToolCallData{
		ToolCallID: tc.ID,
		Name:       tc.Function.Name,
		Arguments:  tc.Function.Arguments,
	})

	execStart := time.Now()
	result, execErr := l.executeTool(ctx, tc.Function.Name, tc.Function.Arguments)
	durationMs := time.Since(execStart).Milliseconds()

	errStr := ""
	if execErr != nil {
		errStr = execErr.Error()
		result = fmt.Sprintf("Error executing tool %q: %s", tc.Function.Name, execErr.Error())
		l.logger.Warn("agent loop tool execution error",
			slog.String("tool", tc.Function.Name), slog.String("error", errStr))
	}

	l.emitEvent(EventToolResult, ToolResultData{
		ToolCallID: tc.ID,
		Name:       tc.Function.Name,
		Result:     result,
		Error:      errStr,
		DurationMs: durationMs,
	})
	l.appendRunningMessages(&schema.Message{
		Role:       schema.Tool,
		Content:    result,
		ToolCallID: tc.ID,
		ToolName:   tc.Function.Name,
	})
}

// executeTool calls the executor and turns a panic into a tool error, so one
// faulty tool cannot take the process down.
func (l *Loop) executeTool(ctx context.Context, name, args string) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tool panicked: %v", r)
			l.emitEvent(EventError, ErrorData{
				Error: fmt.Sprintf("tool %q panicked: %v", name, r),
				Fatal: false,
			})
		}
	}()
	return l.executor.ExecuteTool(ctx, name, args)
}

func (l *Loop) doReflection(ctx context.Context, content string) {
	prompt := l.cfg.ReflectionPrompt
	if prompt == "" {
		prompt = defaultReflectionPrompt
	}
	l.appendRunningMessages(&schema.Message{
		Role:    schema.User,
		Content: prompt + "\n\nYour response was:\n" + content,
	})

	reflection, err := l.callModel(ctx, l.llm, l.GetRunningMessages())
	if err != nil {
		l.emitEvent(EventError, ErrorData{Error: "reflection failed: " + err.Error(), Fatal: false})
		return
	}
	if reflection == nil {
		return
	}
	l.addUsage(reflection)
	l.emitEvent(EventReflection, ReflectionData{Content: reflection.Content})
	l.appendRunningMessages(reflection)
}

// fail ends the run on a fatal error.
func (l *Loop) fail(msg string) {
	l.mu.Lock()
	l.state = StateError
	l.lastError = msg
	l.mu.Unlock()
	l.emitEvent(EventError, ErrorData{Error: msg, Fatal: true})
	l.emitDone(DoneReasonError, "")
}

func (l *Loop) finishStopped() {
	l.logger.Info("agent loop received stop signal")
	l.setState(StateStopped)
	l.emitDone(DoneReasonStopped, "")
}

func (l *Loop) finishRun() {
	// Every run ends in exactly one done event, whatever path ended it.
	l.emitDone(DoneReasonStopped, "")

	l.mu.Lock()
	if l.state == StateRunning {
		l.state = StateDone
	}
	l.mu.Unlock()

	l.evMu.Lock()
	l.eventClosed = true
	close(l.eventCh)
	l.evMu.Unlock()

	close(l.done)
}
