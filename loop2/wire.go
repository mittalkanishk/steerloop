package loop2

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/loop2/internal/audit"
	"github.com/mittalkanishk/steerloop/loop2/internal/control"
	"github.com/mittalkanishk/steerloop/loop2/internal/defaults"
	"github.com/mittalkanishk/steerloop/loop2/internal/dispatch"
	"github.com/mittalkanishk/steerloop/loop2/internal/emitter"
	"github.com/mittalkanishk/steerloop/loop2/internal/engine"
	"github.com/mittalkanishk/steerloop/loop2/internal/modelcall"
	"github.com/mittalkanishk/steerloop/loop2/internal/reflection"
	"github.com/mittalkanishk/steerloop/loop2/internal/state"
	stoppingPolicy "github.com/mittalkanishk/steerloop/loop2/internal/stopping_policy"
	"github.com/mittalkanishk/steerloop/loop2/internal/transcript"
	"github.com/mittalkanishk/steerloop/loop2/pause"
)

// New builds a Loop. It is the only place that picks a concrete type for
// each role, so this function alone tells you what runs. llm is any Eino
// tool-calling chat model; when tools is not empty it is bound to them.
// A nil logger discards log output.
func New(cfg Config, llm model.ToolCallingChatModel, tools []*schema.ToolInfo, executor ToolExecutor, logger *slog.Logger) (*Loop, error) {
	if executor == nil {
		return nil, errors.New("loop2: tool executor not configured")
	}
	if llm == nil {
		return nil, errors.New("loop2: LLM not configured")
	}
	if logger == nil {
		logger = defaults.DiscardLogger()
	}

	base := llm

	if len(tools) > 0 {
		bound, err := llm.WithTools(tools)
		if err != nil {
			return nil, fmt.Errorf("loop2: WithTools: %w", err)
		}
		if bound == nil {
			return nil, errors.New("loop2: WithTools returned no model")
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
		return nil, fmt.Errorf("loop2: %w", err)
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
