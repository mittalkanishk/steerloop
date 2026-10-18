package runcmd

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/cli"
	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/interrupt"
	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/render"
	"github.com/mittalkanishk/steerloop/cmd/steerloop2/internal/steering"
	"github.com/mittalkanishk/steerloop/internal/eventlog"
	"github.com/mittalkanishk/steerloop/internal/provider"
	"github.com/mittalkanishk/steerloop/loop2"
)

// Wire builds a Run from options. It is the only place that picks a concrete
// type for each interface run.go declares, so this function alone tells you
// which implementation runs.
func Wire(ctx context.Context, env cli.Environment, opts Options) (*Run, error) {

	llm, err := provider.Build(ctx, opts.Model, provider.Options{Getenv: env.Getenv, MaxTokens: 4096})
	if err != nil {
		return nil, err
	}

	humanAvailable := env.StdinIsTerminal()

	tools, err := NewToolset(opts, humanAvailable)
	if err != nil {
		return nil, err
	}

	cfg := loop2.DefaultConfig()
	cfg.MaxIterations = opts.MaxIterations
	cfg.Pauses = tools.Gates

	loop, err := loop2.New(cfg, llm.Chat, tools.Infos, tools.Executor, nil)
	if err != nil {
		return nil, err
	}

	// EventSink: JSONL for machines, text for people.
	var sink EventSink
	if opts.Output == "jsonl" {
		sink = eventlog.NewJSONL(env.Stdout())
	} else {
		sink = render.NewText(env.Stderr(), env.Stdout())
	}

	// SteeringSource: the terminal when a person is there, nothing otherwise.
	var steer SteeringSource = steering.None{}
	if humanAvailable {
		steer = steering.NewTerminal(env.Stdin(), env.Stderr())
	}

	return &Run{
		loop:       loop,
		messages:   []*schema.Message{{Role: schema.User, Content: strings.TrimSpace(opts.Prompt)}},
		sink:       sink,
		steering:   steer,
		interrupts: interrupt.NewWatcher(env.Stderr()),
	}, nil
}
