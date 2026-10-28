// Package loop is a controllable agent loop for CloudWeGo Eino, in an
// interface-first, nested shape. Read in this order:
//
//	loop.go                     the public Loop: what a caller can do
//	wire.go                     which concrete type fills each role
//	internal/engine/run.go      the loop itself, and every role it needs
//	then any internal package wire.go names
package loop

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop/event"
	"github.com/mittalkanishk/steerloop/loop/internal/audit"
	"github.com/mittalkanishk/steerloop/loop/internal/control"
	"github.com/mittalkanishk/steerloop/loop/internal/defaults"
	"github.com/mittalkanishk/steerloop/loop/internal/dispatch"
	"github.com/mittalkanishk/steerloop/loop/internal/emitter"
	"github.com/mittalkanishk/steerloop/loop/internal/engine"
	"github.com/mittalkanishk/steerloop/loop/internal/modelcall"
	"github.com/mittalkanishk/steerloop/loop/internal/reflection"
	"github.com/mittalkanishk/steerloop/loop/internal/state"
	stoppingPolicy "github.com/mittalkanishk/steerloop/loop/internal/stopping_policy"
	"github.com/mittalkanishk/steerloop/loop/internal/transcript"
	"github.com/mittalkanishk/steerloop/loop/pause"
)

// ToolExecutor runs the tools the model asks for. An error is not fatal: the
// model receives it as the tool's result and can react.
type ToolExecutor interface {
	ExecuteTool(ctx context.Context, name, arguments string) (string, error)
}

// ConfigChange changes settings while the loop runs. Nil fields are left
// unchanged.
type ConfigChange = state.Change

// State is the lifecycle state of a Loop.
type State = state.Status

const (
	StateIdle    = state.Idle
	StateRunning = state.Running
	StateDone    = state.Done
	StateStopped = state.Stopped
	StateError   = state.Error
)

// Loop runs one agent conversation: call the model, run the tools it asks
// for, feed the results back, repeat. Start it once; it is safe for
// concurrent use. New (wire.go) builds it.
type Loop struct {
	engine     *engine.Engine
	finish     *engine.Finisher
	state      *state.Run
	emitter    *emitter.Emitter
	inbox      *control.Inbox
	transcript *transcript.Logger
}

// New builds a Loop. It is the only place that picks a concrete type for
// each role, so this function alone tells you what runs. llm is any Eino
// tool-calling chat model; when tools is not empty it is bound to them.
// A nil logger discards log output.
func New(cfg Config, llm model.ToolCallingChatModel, tools []*schema.ToolInfo, executor ToolExecutor, logger *slog.Logger) (*Loop, error) {
	if executor == nil {
		return nil, errors.New("loop: tool executor not configured")
	}
	if llm == nil {
		return nil, errors.New("loop: LLM not configured")
	}
	if logger == nil {
		logger = defaults.DiscardLogger()
	}

	base := llm

	if len(tools) > 0 {
		bound, err := llm.WithTools(tools)
		if err != nil {
			return nil, fmt.Errorf("loop: WithTools: %w", err)
		}
		if bound == nil {
			return nil, errors.New("loop: WithTools returned no model")
		}
		llm = bound
	}

	// State: the only shared mutable data.
	st := state.New(state.Settings{
		MaxIterations: cfg.MaxIterations,
		Temperature:   cfg.Temperature,
	})

	// Events: the channel, its sink, the done-event guarantee.
	var sink emitter.Sink = defaults.NoSink{}
	if cfg.EventSink != nil {
		sink = cfg.EventSink
	}
	em := emitter.New(emitter.Options{
		Buffer:       positiveOr(cfg.EventBuffer, defaultEventBuffer),
		StreamEvents: cfg.StreamEvents,
		DoneGrace:    durationOr(cfg.DoneDeliveryGrace, defaultDoneDeliveryGrace),
	}, sink, st, st, logger)

	// Controls, and the one way a run ends.
	inbox := control.New(st)
	finish := engine.NewFinisher(st, em, logger)

	// Model calls: retries, timeouts, streaming.
	var options modelcall.OptionsBuilder = defaults.TemperatureOnly{}
	if cfg.CallOptions != nil {
		options = cfg.CallOptions
	}
	var classifier modelcall.ErrorClassifier = defaults.Classifier{}
	if cfg.ErrorClassifier != nil {
		classifier = cfg.ErrorClassifier
	}
	caller := modelcall.New(modelcall.Timing{
		IterationTimeout:         cfg.IterationTimeout,
		DecoupleIterationTimeout: !cfg.DisableIterationTimeoutDecoupling,
		IdleChunkTimeout:         durationOr(cfg.IdleChunkTimeout, defaultIdleChunkTimeout),
		RetryAttempts:            retryAttempts(cfg.RetryMaxAttempts),
		Streaming:                !cfg.DisableStreaming,
	}, st, options, classifier, logger, st)

	// Tool calls: the executor, or a pause handler for the tools that have one.
	handlers := map[string]pause.Handler{}
	for name, h := range cfg.Pauses {
		if h != nil {
			handlers[name] = h
		}
	}
	dispatcher := dispatch.New(dispatch.Deps{
		Executor: executor,
		Handlers: handlers,
		Emitter:  em,
		History:  st,
		Caller:   caller,
		Replies:  inbox,
		Ender:    finish,
		Model:    llm,
		Tools:    tools,
		Logger:   logger,
	})

	// The stop condition, decided once.
	stop := stopPolicy(cfg)
	reviewer, err := reflectionPolicy(cfg, caller, base)
	if err != nil {
		return nil, fmt.Errorf("loop: %w", err)
	}

	// Audit records of each turn.
	var identity audit.ModelIdentifier = defaults.NoIdentity{}
	if cfg.ModelIdentity != nil {
		identity = cfg.ModelIdentity
	}
	var cost audit.CostEstimator = defaults.ZeroCost{}
	if cfg.CostEstimator != nil {
		cost = cfg.CostEstimator
	}

	var compactor engine.HistoryCompactor = defaults.FullHistory{}
	if cfg.HistoryCompactor != nil {
		compactor = cfg.HistoryCompactor
	}

	log := transcript.New(logger, cfg.TranscriptLogLevel)

	eng := engine.New(engine.Deps{
		Model:            llm,
		Caller:           caller,
		Tools:            dispatcher,
		Stop:             stop,
		Inbox:            inbox,
		Compactor:        compactor,
		State:            st,
		Emitter:          em,
		Finish:           finish,
		Audit:            audit.NewRecorder(identity, cost, stop.Label()),
		Transcript:       log,
		Logger:           logger,
		TotalTimeout:     cfg.TotalTimeout,
		Reviewer:         reviewer,
		MaxReopens:       positiveOr(cfg.MaxReopens, defaultMaxReopens),
		StopInstructions: stopInstructions(cfg, stop),
	})

	return &Loop{
		engine:     eng,
		finish:     finish,
		state:      st,
		emitter:    em,
		inbox:      inbox,
		transcript: log,
	}, nil
}

func stopPolicy(cfg Config) engine.StopPolicy {
	switch cfg.StopCondition {
	case StopOnFinishTool:
		nudge := ""
		if !cfg.DisableAutoNudge {
			nudge = cfg.AutoNudgeText
			if nudge == "" {
				nudge = defaultAutoNudge
			}
		}
		return stoppingPolicy.NewFinishTool(FinishToolName, nudge)
	case StopOnExternal:
		return stoppingPolicy.ExternalFinish{}
	default:
		return stoppingPolicy.NoToolFinish{}
	}
}

// Start begins the run and returns the event channel, which is closed after
// the done event. The loop keeps its own copy of messages. A second call
// returns a channel holding one fatal error.
func (l *Loop) Start(ctx context.Context, messages []*schema.Message) <-chan event.Event {

	messages = l.engine.Seed(messages) // seed the initial message + stop instructions from the chosen stop policy.

	if !l.state.Begin(messages) {
		return failedStart("loop already started")
	}

	l.transcript.Seed(ctx, messages)

	go l.engine.Run(ctx)

	return l.emitter.Events()
}

// Stop asks the loop to stop at the next iteration boundary, or at once if a
// pause handler is waiting for a reply. It cannot be lost to a full queue.
func (l *Loop) Stop() { l.inbox.Stop() }

// Inject queues a user message. It returns false when the queue is full.
func (l *Loop) Inject(message string) bool {
	return l.inbox.Send(control.Control{Kind: control.Inject, Message: message})
}

// ModifyConfig queues a settings change. It returns false when the queue is full.
func (l *Loop) ModifyConfig(c ConfigChange) bool {
	return l.inbox.Send(control.Control{Kind: control.ModifyConfig, Change: c})
}

// Result returns the outcome. ok is false until the loop has ended.
func (l *Loop) Result() (event.DoneData, bool) { return l.state.Result() }

// Wait blocks until the run has ended and the event channel is closed.
func (l *Loop) Wait() { l.finish.Wait() }

// Events returns the event channel, the same one Start returns.
func (l *Loop) Events() <-chan event.Event { return l.emitter.Events() }

// Iteration returns the number of model calls started so far.
func (l *Loop) Iteration() int { return l.state.Iteration() }

// Messages returns a copy of the conversation as it stands now.
func (l *Loop) Messages() []*schema.Message { return l.state.Messages() }

// State returns the lifecycle state.
func (l *Loop) State() State { return l.state.Status() }

func failedStart(msg string) <-chan event.Event {
	ch := make(chan event.Event, 1)
	ch <- event.New(event.ErrorData{Error: msg, Fatal: true}, 0)
	close(ch)
	return ch
}

// retryAttempts turns RetryMaxAttempts into a count of extra attempts.
func retryAttempts(configured int) int {
	switch {
	case configured < 0:
		return 0
	case configured == 0:
		return 1
	default:
		return configured
	}
}

func stopInstructions(cfg Config, stop engine.StopPolicy) string {
	if cfg.StopInstructions != "" {
		return cfg.StopInstructions
	}
	return stop.Instructions()
}

func reflectionPolicy(cfg Config, caller *modelcall.Caller, base model.ToolCallingChatModel) (engine.Reviewer, error) {
	switch cfg.Reflect {
	case ReflectOnIteration:
		judge, err := reflection.NewJudge(caller, base, stringOr(cfg.IterationReviewPrompt, defaultIterationReviewPrompt))
		if err != nil {
			return nil, err
		}
		return reflection.NewOnIteration(judge), nil
	case ReflectOnFinish:
		judge, err := reflection.NewJudge(caller, base, stringOr(cfg.FinishReviewPrompt, defaultFinishReviewPrompt))
		if err != nil {
			return nil, err
		}
		return reflection.NewOnFinish(judge), nil
	default:
		return reflection.Off{}, nil
	}
}
