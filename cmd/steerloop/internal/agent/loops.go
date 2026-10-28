package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/cmd/steerloop/internal/cli"
	"github.com/mittalkanishk/steerloop/internal/provider"
	"github.com/mittalkanishk/steerloop/loop"
)

// Loops builds the loop for a turn. A loop runs once, so a command that runs
// several turns asks for a fresh one each time, all with the same settings
// and the same model; the command carries the conversation over.
type Loops struct {
	cfg      loop.Config
	chat     model.ToolCallingChatModel
	tools    []*schema.ToolInfo
	executor loop.ToolExecutor
	logger   *slog.Logger // nil discards the loop's debug log
}

// NewLoops connects to the model and loads the tools. humanAvailable says
// whether someone can answer an ask_user or confirm question.
func NewLoops(ctx context.Context, env cli.Environment, opts Options, humanAvailable bool) (Loops, error) {

	llm, err := provider.Build(ctx, opts.Model, provider.Options{Getenv: env.Getenv, MaxTokens: opts.MaxTokens})
	if err != nil {
		return Loops{}, err
	}

	tools, err := NewToolset(opts, humanAvailable)
	if err != nil {
		return Loops{}, err
	}

	cfg := loop.DefaultConfig()
	cfg.MaxIterations = opts.MaxIterations
	cfg.Pauses = tools.Gates
	cfg.StopCondition = stopConditions[opts.StopCondition] // basically a map of 3 accepted values.
	cfg.Reflect = reflectConditions[opts.Reflect]
	cfg.Temperature = opts.Temperature
	cfg.IterationTimeout = opts.IterationTimeout
	cfg.TotalTimeout = opts.TotalTimeout

	// Logger: the loop's debug log on stderr with --debug; nil discards it.
	var logger *slog.Logger
	if opts.Debug {
		logger = slog.New(slog.NewTextHandler(env.Stderr(), &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	return Loops{cfg: cfg, chat: llm.Chat, tools: tools.Infos, executor: tools.Executor, logger: logger}, nil
}

// New builds a loop, ready to start.
func (f Loops) New() (*loop.Loop, error) {
	return loop.New(f.cfg, f.chat, f.tools, f.executor, f.logger)
}

// StopCondition is how every loop ends, which the trace shows.
func (f Loops) StopCondition() loop.StopCondition {
	return f.cfg.StopCondition
}

// InitialMessages is the conversation the first turn starts from: the
// --system-prompt file, if any, then the user prompt, if any.
func InitialMessages(opts Options) ([]*schema.Message, error) {
	var messages []*schema.Message
	if opts.SystemPromptPath != "" {
		system, err := os.ReadFile(opts.SystemPromptPath)
		if err != nil {
			return nil, fmt.Errorf("read system prompt: %w", err)
		}
		if strings.TrimSpace(string(system)) == "" {
			return nil, fmt.Errorf("system prompt file %s is empty", opts.SystemPromptPath)
		}
		messages = append(messages, &schema.Message{
			Role:    schema.System,
			Content: string(system),
		})
	}

	// So you get [system, user] with --system-prompt and [user] without it;
	// chat without --user-prompt starts from [system] or nothing.
	if prompt := strings.TrimSpace(opts.UserPrompt); prompt != "" {
		messages = append(messages, &schema.Message{
			Role:    schema.User,
			Content: prompt,
		})
	}

	return messages, nil
}
