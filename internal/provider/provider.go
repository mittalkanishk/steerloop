// Package provider builds an Eino chat model from a "provider:model" string.
package provider

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"github.com/Kanishkmittal55/steerloop/steerlooptest"
)

// Model is a chat model with the names used to label it in events.
type Model struct {
	Chat     model.ToolCallingChatModel
	Provider string
	Name     string
}

// Options are the settings that are not part of the model string.
type Options struct {
	// Getenv reads an environment variable. API keys are only ever read here.
	Getenv func(string) string
	// MaxTokens caps the reply length for providers that require a cap.
	MaxTokens int
}

// Build parses spec and returns the model. Supported forms:
//
//	anthropic:<model>   key from ANTHROPIC_API_KEY
//	openai:<model>      key from OPENAI_API_KEY, optional OPENAI_BASE_URL
//	script:<path>       a JSON script played back with no network access
//
// A missing key is reported before any network call is made.
func Build(ctx context.Context, spec string, opts Options) (*Model, error) {
	kind, name, ok := strings.Cut(spec, ":")
	if !ok || name == "" {
		return nil, fmt.Errorf("model %q must be provider:model, for example anthropic:claude-sonnet-4-5", spec)
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 4096
	}

	switch kind {
	case "script":
		steps, err := steerlooptest.LoadScript(name)
		if err != nil {
			return nil, err
		}
		return &Model{Chat: steerlooptest.NewScriptedModel(steps...), Provider: "script", Name: filepath.Base(name)}, nil

	case "anthropic":
		key := opts.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set")
		}
		chat, err := claude.NewChatModel(ctx, &claude.Config{APIKey: key, Model: name, MaxTokens: opts.MaxTokens})
		if err != nil {
			return nil, fmt.Errorf("anthropic model: %w", err)
		}
		return &Model{Chat: chat, Provider: "anthropic", Name: name}, nil

	case "openai":
		key := opts.Getenv("OPENAI_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is not set")
		}
		chat, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
			APIKey:  key,
			Model:   name,
			BaseURL: opts.Getenv("OPENAI_BASE_URL"),
		})
		if err != nil {
			return nil, fmt.Errorf("openai model: %w", err)
		}
		return &Model{Chat: chat, Provider: "openai", Name: name}, nil

	default:
		return nil, fmt.Errorf("unknown provider %q: use anthropic, openai or script", kind)
	}
}
