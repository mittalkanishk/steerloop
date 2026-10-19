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
	"github.com/mittalkanishk/steerloop/internal/tools"
	"github.com/mittalkanishk/steerloop/internal/provider"
)

const askUserToolName = "ask_user"

var stopConditions = map[string]steerloop.StopCondition{
	"no-tool-calls": steerloop.StopOnNoToolCalls,
	"finish-tool":   steerloop.StopOnFinishTool,
	"explicit":      steerloop.StopOnExplicit,
}

func runCommand(e env, args []string) int {

	var opts runOptions

	flags := runFlags(e.stderr, &opts)

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage // the flag package has already reported the problem
	}

	leftoverFlagArgs := flags.Args()
	if len(leftoverFlagArgs) > 0 {
		return usageError(e.stderr, fmt.Errorf("unexpected argument %q: give the prompt with --prompt", strings.Join(leftoverFlagArgs, " ")))
	}

	if err := opts.validate(); err != nil {
		return usageError(e.stderr, err)
	}

	stop := stopConditions[opts.stopName]
	prompt := strings.TrimSpace(opts.prompt)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	llm, err := provider.Build(ctx, opts.model, provider.Options{Getenv: e.getenv, MaxTokens: opts.maxTokens})
	if err != nil {
		return usageError(e.stderr, err)
	}

	// Someone can answer the model's questions at the terminal, or through
	// the control file.
	interactive := e.stdinIsTerminal && e.stdin != nil
	humanAvailable := interactive || opts.controlPath != ""

	tools, err := buildToolset(opts, stop, humanAvailable)
	if err != nil {
		return usageError(e.stderr, err)
	}

	var messages []*schema.Message
	if opts.systemPromptPath != "" {
		systemPrompt, err := os.ReadFile(opts.systemPromptPath)
		if err != nil {
			return usageError(e.stderr, fmt.Errorf("read system prompt: %w", err))
		}
		messages = append(messages, &schema.Message{Role: schema.System, Content: string(systemPrompt)})
	}

	messages = append(messages, &schema.Message{Role: schema.User, Content: prompt})

	emit, closeOutputs, err := openOutputs(e, opts)
	if err != nil {
		return usageError(e.stderr, err)
	}
	defer closeOutputs()

	var controlFile *os.File

	if opts.controlPath != "" {
		if controlFile, err = os.Open(opts.controlPath); err != nil {
			return usageError(e.stderr, fmt.Errorf("open control file: %w", err))
		}
		defer controlFile.Close()
	}

	var logger *slog.Logger
	if opts.verbose {
		logger = slog.New(slog.NewTextHandler(e.stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	// A terminal with no control file stays up after an answer and reads the
	// next prompt. Anything else is one turn.
	multiTurn := interactive && controlFile == nil
	var terminalLines <-chan string

	if multiTurn {
		terminalLines = scanLines(e.stdin)
	}

	emit(headerRecord(runHeader{
		Version:       moduleVersion(),
		Model:         opts.model,
		Stop:          opts.stopName,
		MaxIterations: opts.maxIterations,
		Tools:         tools.names,
		Prompt:        prompt,
	}))

	interrupts := watchInterrupts(ctx, cancel, e.signals, e.stderr)

	cfg := loopConfig(opts, stop, llm, tools.pauses)

	for {

		loop, err := steerloop.New(cfg, llm.Chat, tools.infos, tools.executor, logger)
		if err != nil {
			return usageError(e.stderr, err)
		}

		interrupts.running.Store(loop)
		events := loop.Start(ctx, messages)

		steer := &controller{
			loop:   loop,
			stderr: e.stderr,
		}

		// Steering comes from the control file, or from lines typed at the
		// terminal while the turn runs.
		var stopForwarding, forwardingDone chan struct{}
		if controlFile != nil {
			go steer.readControls(ctx, controlFile)
		} else if multiTurn {
			stopForwarding = make(chan struct{})
			forwardingDone = make(chan struct{})
			go func() {
				defer close(forwardingDone)
				steer.forwardLines(ctx, terminalLines, stopForwarding)
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

		if stopForwarding != nil {
			close(stopForwarding)
			<-forwardingDone
		}

		if interrupts.wasAborted() {
			return exitStopped
		}

		result, _ := loop.Result()
		if result.Reason != steerloop.DoneReasonFinished || !multiTurn {
			return exitCodeFor(result.Reason)
		}

		// The answer is in. Wait for the next prompt and carry the
		// conversation forward into a fresh loop.
		interrupts.atPrompt.Store(true)
		interrupts.running.Store(nil)
		nextPrompt, ok := readNextPrompt(ctx, e.stderr, terminalLines)
		interrupts.atPrompt.Store(false)
		if !ok {
			if ctx.Err() != nil {
				return exitStopped
			}
			return exitOK
		}

		messages = append(loop.GetRunningMessages(), &schema.Message{Role: schema.User, Content: nextPrompt})
	}
}

// runFlags declares the flags of `steerloop run` and binds them to opts.
func runFlags(stderr io.Writer, opts *runOptions) *flag.FlagSet {
	flags := flag.NewFlagSet("steerloop run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.prompt, "prompt", "", "the prompt for the first turn (required)")
	flags.StringVar(&opts.model, "model", "", "anthropic:<model>, openai:<model> or script:<path> (required)")
	flags.StringVar(&opts.manifestPath, "tools", "", "path to a tool manifest (YAML)")
	flags.StringVar(&opts.systemPromptPath, "system", "", "path to a system prompt file")
	flags.StringVar(&opts.stopName, "stop", "no-tool-calls", "when the loop ends: no-tool-calls, finish-tool or explicit")
	flags.IntVar(&opts.maxIterations, "max-iterations", 12, "cap on model calls")
	flags.DurationVar(&opts.stepTimeout, "step-timeout", time.Minute, "time allowed for one model call")
	flags.DurationVar(&opts.totalTimeout, "total-timeout", 10*time.Minute, "time allowed for the whole run")
	flags.Float64Var(&opts.temperature, "temperature", -1, "sampling temperature; below 0 leaves it to the provider")
	flags.IntVar(&opts.maxTokens, "max-tokens", 4096, "cap on reply length, for providers that need one")
	flags.BoolVar(&opts.askUser, "ask", false, "give the model an ask_user tool that asks on the terminal")
	flags.BoolVar(&opts.autoApprove, "yes", false, "approve tools marked confirm without asking")
	flags.StringVar(&opts.outputFormat, "output", "text", "text or jsonl")
	flags.StringVar(&opts.recordPath, "record", "", "also write every event to this JSONL file")
	flags.StringVar(&opts.controlPath, "control", "", "read JSONL controls from this file or FIFO")
	flags.BoolVar(&opts.verbose, "verbose", false, "write the loop's debug log to stderr")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "usage: steerloop run --model <spec> --prompt <text> [flags]\n\nThe prompt is always given with --prompt; stdin stays free for answering the run.")
		flags.PrintDefaults()
	}
	return flags
}

// validate rejects flag values the loop cannot run with.
func (o runOptions) validate() error {
	_, knownStop := stopConditions[o.stopName]
	switch {
	case o.model == "":
		return errors.New("--model is required")
	case strings.TrimSpace(o.prompt) == "":
		return errors.New("--prompt is required")
	case !knownStop:
		return errors.New("--stop must be no-tool-calls, finish-tool or explicit")
	case o.outputFormat != "text" && o.outputFormat != "jsonl":
		return errors.New("--output must be text or jsonl")
	case o.maxIterations < 1:
		return errors.New("--max-iterations must be at least 1")
	}
	return nil
}

// usageError reports err the way every refusal of this command is reported
// and returns the exit code for it.
func usageError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "steerloop: %v\n", err)
	return exitUsage
}

func buildToolset(opts runOptions, stop steerloop.StopCondition, humanAvailable bool) (toolset, error) {
	tools := toolset{
		pauses: map[string]steerloop.PauseFunc{},
		executor: steerloop.ToolExecutorFunc(func(_ context.Context, name, _ string) (string, error) {
			return "", fmt.Errorf("unknown tool %q", name)
		}),
	}

	var manifest *exectool.ToolManifestExecutor
	if opts.manifestPath != "" {
		var err error
		if manifest, err = exectool.Load(opts.manifestPath); err != nil {
			return toolset{}, fmt.Errorf("invalid manifest:\n%w", err)
		}
		if tools.infos, err = manifest.ToolInfos(); err != nil {
			return toolset{}, err
		}
		tools.executor = manifest
		for _, t := range manifest.Tools {
			tools.names = append(tools.names, t.Name)
			if t.RequiresConfirmation {
				tools.pauses[t.Name] = confirmPause(opts.autoApprove, humanAvailable)
			}
		}
	}

	if opts.askUser {
		if manifest != nil {
			if _, declared := manifest.Tool(askUserToolName); declared {
				return toolset{}, fmt.Errorf("the manifest declares %q, which --ask also provides", askUserToolName)
			}
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
			return toolset{}, err
		}
		tools.infos = append(tools.infos, info)
		tools.names = append(tools.names, askUserToolName)
		tools.pauses[askUserToolName] = askPause(humanAvailable)
	}

	if stop == steerloop.StopOnFinishTool {
		info, err := steerloop.NewToolInfo(steerloop.FinishToolName, "Call this once the task is complete, with the final result.", map[string]any{
			"type":       "object",
			"properties": map[string]any{"result": map[string]any{"type": "string", "description": "The final result."}},
			"required":   []string{"result"},
		})
		if err != nil {
			return toolset{}, err
		}
		tools.infos = append(tools.infos, info)
	}

	return tools, nil
}

func loopConfig(opts runOptions, stop steerloop.StopCondition, llm *provider.Model, pauses map[string]steerloop.PauseFunc) steerloop.LoopConfig {
	cfg := steerloop.DefaultConfig()
	cfg.MaxIterations = opts.maxIterations
	cfg.StopCondition = stop
	cfg.StepTimeout = opts.stepTimeout
	cfg.TotalTimeout = opts.totalTimeout
	cfg.EventBuffer = 1024
	cfg.Pauses = pauses
	cfg.ModelIdentity = func() (string, string) { return llm.Provider, llm.Name }
	if opts.temperature < 0 {
		// No temperature was asked for, so none is sent and the provider's
		// default applies.
		cfg.CallOptions = func(float32) []model.Option { return nil }
	} else {
		cfg.Temperature = opts.temperature
	}
	return cfg
}

// openOutputs wires up where records go: the terminal renderer or JSONL on
// stdout, plus the --record file when there is one. closeOutputs releases
// the file.
func openOutputs(e env, opts runOptions) (emit func(record), closeOutputs func(), err error) {
	var sinks []func(record)
	if opts.outputFormat == "jsonl" {
		out := &jsonlWriter{w: e.stdout}
		sinks = append(sinks, func(r record) { _ = out.write(r) })
	} else {
		renderer := &textRenderer{trace: e.stderr, final: e.stdout}
		sinks = append(sinks, renderer.render)
	}

	closeOutputs = func() {}
	if opts.recordPath != "" {
		f, err := os.OpenFile(opts.recordPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("open record file: %w", err)
		}
		closeOutputs = func() { _ = f.Close() }
		recording := &jsonlWriter{w: f}
		sinks = append(sinks, func(r record) { _ = recording.write(r) })
	}

	emit = func(r record) {
		for _, sink := range sinks {
			sink(r)
		}
	}
	return emit, closeOutputs, nil
}

func watchInterrupts(ctx context.Context, cancel context.CancelFunc, signals <-chan os.Signal, stderr io.Writer) *interruptWatcher {
	w := &interruptWatcher{aborted: make(chan struct{})}
	if signals == nil {
		return w
	}
	go func() {
		select {
		case <-signals:
			if w.atPrompt.Load() {
				cancel()
				return
			}
			fmt.Fprintln(stderr, "\nstopping after the current step (interrupt again to abort)")
			if loop := w.running.Load(); loop != nil {
				loop.Stop()
			}
		case <-ctx.Done():
			return
		}
		select {
		case <-signals:
			close(w.aborted)
			cancel()
		case <-ctx.Done():
		}
	}()
	return w
}

func (w *interruptWatcher) wasAborted() bool {
	select {
	case <-w.aborted:
		return true
	default:
		return false
	}
}

// readNextPrompt blocks until the next non-empty line, EOF, or ctx cancel.
func readNextPrompt(ctx context.Context, stderr io.Writer, lines <-chan string) (string, bool) {
	for {
		fmt.Fprint(stderr, "> ")
		select {
		case <-ctx.Done():
			return "", false
		case line, ok := <-lines:
			if !ok {
				return "", false
			}
			if line = strings.TrimSpace(line); line != "" {
				return line, true
			}
		}
	}
}

// askPause answers an ask_user call with the next line of input. With nobody
// to ask it ends the run as awaiting a human.
func askPause(humanAvailable bool) steerloop.PauseFunc {
	return func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
		var args struct {
			Question string   `json:"question"`
			Options  []string `json:"options"`
		}
		_ = json.Unmarshal([]byte(in.Arguments), &args)
		if strings.TrimSpace(args.Question) == "" {
			return askUserToolName + " needs a non-empty \"question\".", true
		}
		in.Emit(steerloop.EventAwaitingInput, steerloop.AwaitingInputData{Question: args.Question, Options: args.Options})
		if !humanAvailable {
			return "", false
		}
		return in.Block(ctx)
	}
}

// confirmPause asks before running a tool marked confirm. With nobody to ask
// the tool is not run.
func confirmPause(autoApprove, humanAvailable bool) steerloop.PauseFunc {
	run := func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
		out, err := in.Execute(ctx, in.Name, in.Arguments)
		if err != nil {
			return fmt.Sprintf("Error executing tool %q: %s", in.Name, err.Error()), true
		}
		return out, true
	}
	return func(ctx context.Context, in steerloop.PauseIn) (string, bool) {
		if autoApprove {
			return run(ctx, in)
		}
		if !humanAvailable {
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
