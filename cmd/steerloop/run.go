package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/mittalkanishk/steerloop"
	"github.com/mittalkanishk/steerloop/internal/exectool"
	"github.com/mittalkanishk/steerloop/internal/provider"
)

const askUserToolName = "ask_user"

var stopConditions = map[string]steerloop.StopCondition{
	"no-tool-calls": steerloop.StopOnNoToolCalls,
	"finish-tool":   steerloop.StopOnFinishTool,
	"explicit":      steerloop.StopOnExplicit,
}

type runOptions struct {
	model, tools, system, stop, output, record, control string
	maxIterations, maxTokens                            int
	stepTimeout, totalTimeout                           time.Duration
	temperature                                         float64
	ask, yes, verbose                                   bool
}

func runCommand(e env, args []string) int {
	var o runOptions
	fs := flag.NewFlagSet("steerloop run", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.StringVar(&o.model, "model", "", "anthropic:<model>, openai:<model> or script:<path> (required)")
	fs.StringVar(&o.tools, "tools", "", "path to a tool manifest (YAML)")
	fs.StringVar(&o.system, "system", "", "path to a system prompt file")
	fs.StringVar(&o.stop, "stop", "no-tool-calls", "when the loop ends: no-tool-calls, finish-tool or explicit")
	fs.IntVar(&o.maxIterations, "max-iterations", 12, "cap on model calls")
	fs.DurationVar(&o.stepTimeout, "step-timeout", time.Minute, "time allowed for one model call")
	fs.DurationVar(&o.totalTimeout, "total-timeout", 10*time.Minute, "time allowed for the whole run")
	fs.Float64Var(&o.temperature, "temperature", -1, "sampling temperature; below 0 leaves it to the provider")
	fs.IntVar(&o.maxTokens, "max-tokens", 4096, "cap on reply length, for providers that need one")
	fs.BoolVar(&o.ask, "ask", false, "give the model an ask_user tool that asks on the terminal")
	fs.BoolVar(&o.yes, "yes", false, "approve tools marked confirm without asking")
	fs.StringVar(&o.output, "output", "text", "text or jsonl")
	fs.StringVar(&o.record, "record", "", "also write every event to this JSONL file")
	fs.StringVar(&o.control, "control", "", "read JSONL controls from this file or FIFO")
	fs.BoolVar(&o.verbose, "verbose", false, "write the loop's debug log to stderr")
	fs.Usage = func() {
		fmt.Fprintln(e.stderr, "usage: steerloop run [flags] [prompt]\n\nFlags go before the prompt. With no prompt argument the prompt is read from stdin.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}

	fail := func(format string, a ...any) int {
		fmt.Fprintf(e.stderr, "steerloop: "+format+"\n", a...)
		return exitUsage
	}

	stop, ok := stopConditions[o.stop]
	switch {
	case o.model == "":
		return fail("--model is required")
	case !ok:
		return fail("--stop must be no-tool-calls, finish-tool or explicit")
	case o.output != "text" && o.output != "jsonl":
		return fail("--output must be text or jsonl")
	case o.maxIterations < 1:
		return fail("--max-iterations must be at least 1")
	}

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	promptFromStdin := false
	if prompt == "" && !e.stdinIsTerminal && e.stdin != nil {
		data, err := io.ReadAll(e.stdin)
		if err != nil {
			return fail("read prompt: %v", err)
		}
		prompt = strings.TrimSpace(string(data))
		promptFromStdin = true
	}
	if prompt == "" {
		return fail("a prompt is required, as an argument or on stdin")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	llm, err := provider.Build(ctx, o.model, provider.Options{Getenv: e.getenv, MaxTokens: o.maxTokens})
	if err != nil {
		return fail("%v", err)
	}

	var (
		tools     []*schema.ToolInfo
		toolNames []string
		manifest  *exectool.Manifest
		executor  steerloop.ToolExecutor = steerloop.ToolExecutorFunc(
			func(_ context.Context, name, _ string) (string, error) {
				return "", fmt.Errorf("unknown tool %q", name)
			})
	)
	if o.tools != "" {
		manifest, err = exectool.Load(o.tools)
		if err != nil {
			return fail("invalid manifest:\n%v", err)
		}
		if tools, err = manifest.ToolInfos(); err != nil {
			return fail("%v", err)
		}
		executor = manifest
		for _, t := range manifest.Tools {
			toolNames = append(toolNames, t.Name)
		}
	}

	interactive := e.stdinIsTerminal && !promptFromStdin && e.stdin != nil
	canAnswer := interactive || o.control != ""

	pauses := map[string]steerloop.PauseFunc{}
	if manifest != nil {
		for _, t := range manifest.Tools {
			if t.Confirm {
				pauses[t.Name] = confirmPause(o.yes, canAnswer)
			}
		}
	}
	if o.ask {
		if _, clash := pauses[askUserToolName]; clash || (manifest != nil && hasTool(manifest, askUserToolName)) {
			return fail("the manifest declares %q, which --ask also provides", askUserToolName)
		}
		info, err := steerloop.NewToolInfo(askUserToolName, "Ask the person running this session a question and wait for the answer.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{"type": "string", "description": "The question to ask."},
				"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional suggested answers."},
			},
			"required": []string{"question"},
		})
		if err != nil {
			return fail("%v", err)
		}
		tools = append(tools, info)
		toolNames = append(toolNames, askUserToolName)
		pauses[askUserToolName] = askPause(canAnswer)
	}
	if stop == steerloop.StopOnFinishTool {
		info, err := steerloop.NewToolInfo(steerloop.FinishToolName, "Call this once the task is complete, with the final result.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"result": map[string]any{"type": "string", "description": "The final result."}},
			"required":   []string{"result"},
		})
		if err != nil {
			return fail("%v", err)
		}
		tools = append(tools, info)
	}

	cfg := steerloop.DefaultConfig()
	cfg.MaxIterations = o.maxIterations
	cfg.StopCondition = stop
	cfg.StepTimeout = o.stepTimeout
	cfg.TotalTimeout = o.totalTimeout
	cfg.EventBuffer = 1024
	cfg.Pauses = pauses
	cfg.ModelIdentity = func() (string, string) { return llm.Provider, llm.Name }
	if o.temperature < 0 {
		cfg.CallOptions = func(float32) []model.Option { return nil }
	} else {
		cfg.Temperature = o.temperature
	}

	var messages []*schema.Message
	if o.system != "" {
		data, err := os.ReadFile(o.system)
		if err != nil {
			return fail("read system prompt: %v", err)
		}
		messages = append(messages, &schema.Message{Role: schema.System, Content: string(data)})
	}
	messages = append(messages, &schema.Message{Role: schema.User, Content: prompt})

	var sinks []func(record)
	if o.output == "jsonl" {
		out := &jsonlWriter{w: e.stdout}
		sinks = append(sinks, func(r record) { _ = out.write(r) })
	} else {
		renderer := &textRenderer{trace: e.stderr, final: e.stdout}
		sinks = append(sinks, renderer.render)
	}
	if o.record != "" {
		f, err := os.OpenFile(o.record, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return fail("open record file: %v", err)
		}
		defer f.Close()
		rec := &jsonlWriter{w: f}
		sinks = append(sinks, func(r record) { _ = rec.write(r) })
	}
	emit := func(r record) {
		for _, sink := range sinks {
			sink(r)
		}
	}

	var controlFile *os.File
	if o.control != "" {
		if controlFile, err = os.Open(o.control); err != nil {
			return fail("open control file: %v", err)
		}
		defer controlFile.Close()
	}

	var logger *slog.Logger
	if o.verbose {
		logger = slog.New(slog.NewTextHandler(e.stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	loop, err := steerloop.New(cfg, llm.Chat, tools, executor, logger)
	if err != nil {
		return fail("%v", err)
	}

	emit(headerRecord(runHeader{
		Version:       moduleVersion(),
		Model:         o.model,
		Stop:          o.stop,
		MaxIterations: o.maxIterations,
		Tools:         toolNames,
		Prompt:        prompt,
	}))

	events := loop.Start(ctx, messages)
	ctl := &controller{loop: loop, stderr: e.stderr}
	switch {
	case controlFile != nil:
		go ctl.readControls(ctx, controlFile)
	case interactive:
		go ctl.readLines(ctx, e.stdin)
	}

	aborted := make(chan struct{})
	if e.signals != nil {
		go func() {
			select {
			case <-e.signals:
				fmt.Fprintln(e.stderr, "\nstopping after the current step (interrupt again to abort)")
				loop.Stop()
			case <-ctx.Done():
				return
			}
			select {
			case <-e.signals:
				close(aborted)
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	for evt := range events {
		rec, err := toRecord(evt)
		if err != nil {
			fmt.Fprintf(e.stderr, "steerloop: cannot encode %s event: %v\n", evt.Type, err)
			continue
		}
		emit(rec)
	}

	select {
	case <-aborted:
		return exitStopped
	default:
	}
	result, _ := loop.Result()
	return exitCodeFor(result.Reason)
}

func hasTool(m *exectool.Manifest, name string) bool {
	_, ok := m.Tool(name)
	return ok
}

// askPause answers an ask_user call with the next line of input. With nobody
// to ask it ends the run as awaiting a human.
func askPause(canAnswer bool) steerloop.PauseFunc {
	return func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
		var args struct {
			Question string   `json:"question"`
			Options  []string `json:"options"`
		}
		_ = json.Unmarshal([]byte(in.Arguments), &args)
		if strings.TrimSpace(args.Question) == "" {
			return "ask_user needs a non-empty \"question\".", true
		}
		in.Emit(steerloop.EventAwaitingInput, steerloop.AwaitingInputData{Question: args.Question, Options: args.Options})
		if !canAnswer {
			return "", false
		}
		return in.Block(ctx)
	}
}

// confirmPause asks before running a tool marked confirm. With nobody to ask
// the tool is not run.
func confirmPause(autoYes, canAnswer bool) steerloop.PauseFunc {
	run := func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
		out, err := in.Execute(ctx, in.Name, in.Arguments)
		if err != nil {
			return fmt.Sprintf("Error executing tool %q: %s", in.Name, err.Error()), true
		}
		return out, true
	}
	return func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
		if autoYes {
			return run(ctx, in)
		}
		if !canAnswer {
			return "This tool needs approval and nobody is available to give it. It was not run.", true
		}
		in.Emit(steerloop.EventAwaitingInput, steerloop.AwaitingInputData{
			Question: fmt.Sprintf("Run %s %s?", in.Name, oneLine(in.Arguments, maxResultPreview)),
			Options:  []string{"y", "n"},
		})
		reply, ok := in.Block(ctx)
		if !ok {
			return "", false
		}
		switch strings.ToLower(strings.TrimSpace(reply)) {
		case "y", "yes":
			return run(ctx, in)
		default:
			return "The user declined to run this tool.", true
		}
	}
}
