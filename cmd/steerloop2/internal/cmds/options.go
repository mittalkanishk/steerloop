package runcmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/mittalkanishk/steerloop/loop2"
)

var stopConditions = map[string]loop2.StopCondition{
	"no-tool-calls":   loop2.StopOnNoToolCalls,
	"finish-tool":     loop2.StopOnFinishTool,
	"external-finish": loop2.StopOnExternal,
}

var reflectConditions = map[string]loop2.ReflectCondition{
	"off":       loop2.ReflectOff,
	"on-steps":  loop2.ReflectOnSteps,
	"on-finish": loop2.ReflectOnFinish,
}

// Options is the `steerloop2 run` command line.
type Options struct {
	// Prompt Options
	SystemPromptPath string
	UserPrompt       string
	// Model Options
	Model       string
	Temperature float64 // below 0: not sent, so the provider's default applies
	MaxTokens   int
	// Loop Options
	MaxIterations int
	StepTimeout   time.Duration
	TotalTimeout  time.Duration
	StopCondition string
	Reflect       string // "on-steps" or "on-finish"
	// Tool Options
	ToolsManifestPath string
	AutoApprove       bool
	AskUser           bool
	// Output Options
	Output string
	Debug  bool
}

// maxTemperature is the highest temperature any supported provider accepts
// (OpenAI allows up to 2, Anthropic up to 1).
const maxTemperature = 2

// ParseOptions reads flags into Options and reports its own errors to
// stderr. It rejects leftover arguments: the flag package stops at the first
// one, so `--user-prompt fix the bug --debug` would otherwise run with the
// prompt "fix" and drop the rest without a word.
func ParseOptions(args []string, stderr io.Writer) (Options, error) {
	var opts Options
	flags := flag.NewFlagSet("steerloop2 run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	// Prompt Flags
	flags.StringVar(&opts.SystemPromptPath, "system-prompt", "", "path to a system prompt file")
	flags.StringVar(&opts.UserPrompt, "user-prompt", "", "the user prompt (required)")
	// Model Flags
	flags.StringVar(&opts.Model, "model", "", "anthropic:<model>, openai:<model> or script:<path> (required)")
	flags.Float64Var(&opts.Temperature, "temperature", -1, "sampling temperature; below 0 leaves it to the provider")
	flags.IntVar(&opts.MaxTokens, "max-tokens", 4096, "cap on reply length, for providers that need one")
	// Loop Flags
	flags.IntVar(&opts.MaxIterations, "max-iterations", 12, "cap on model calls")
	flags.DurationVar(&opts.StepTimeout, "step-timeout", time.Minute, "time allowed for one model call")
	flags.DurationVar(&opts.TotalTimeout, "total-timeout", 10*time.Minute, "time allowed for the whole run")
	flags.StringVar(&opts.StopCondition, "stop-condition", "no-tool-calls", "when the loop ends: no-tool-calls, finish-tool or explicit")
	flags.StringVar(&opts.Reflect, "reflect", "off", "when to reflect: off, on-steps or on-finish")
	// Tool Flags
	flags.StringVar(&opts.ToolsManifestPath, "tools", "", "path to a tool manifest (YAML)")
	flags.BoolVar(&opts.AskUser, "ask-user", false, "give the model an ask_user tool")
	flags.BoolVar(&opts.AutoApprove, "skip-confirm", false, "approve tools marked confirm without asking")
	// Output Flags
	flags.StringVar(&opts.Output, "output", "text", "text or jsonl")
	flags.BoolVar(&opts.Debug, "debug", false, "write the loop's debug log to stderr")
	if err := flags.Parse(args); err != nil {
		return opts, err
	}

	if flags.NArg() > 0 {
		err := fmt.Errorf("unexpected argument %q: flags must come before it, and a value with spaces needs quotes", flags.Arg(0))
		fmt.Fprintf(stderr, "steerloop2: %v\n", err)
		return opts, err
	}

	return opts, nil
}

// Validate rejects options the loop cannot run with, and flags that would
// be silently ignored. It reports every problem at once, not just the first.
// Files (--system-prompt, --tools) and the model spec are checked when the
// run is built, still before any network call.
func (o Options) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	// Prompt
	if strings.TrimSpace(o.UserPrompt) == "" {
		fail("--user-prompt is required")
	}

	// Model
	if strings.TrimSpace(o.Model) == "" {
		fail("--model is required")
	}
	if math.IsNaN(o.Temperature) || math.IsInf(o.Temperature, 0) {
		fail("--temperature must be a finite number")
	} else if o.Temperature > maxTemperature {
		fail("--temperature must be at most %d (below 0 leaves it to the provider)", maxTemperature)
	}
	if o.MaxTokens < 1 {
		fail("--max-tokens must be at least 1")
	}

	// Loop
	if o.MaxIterations < 1 {
		fail("--max-iterations must be at least 1")
	}
	if o.StepTimeout < 0 {
		fail("--step-timeout must not be negative (0 means no limit)")
	}
	if o.TotalTimeout < 0 {
		fail("--total-timeout must not be negative (0 means no limit)")
	}
	if o.StepTimeout > 0 && o.TotalTimeout > 0 && o.StepTimeout > o.TotalTimeout {
		fail("--step-timeout (%s) must not exceed --total-timeout (%s)", o.StepTimeout, o.TotalTimeout)
	}
	if _, ok := stopConditions[o.StopCondition]; !ok {
		fail("--stop-condition must be no-tool-calls, finish-tool or explicit, not %q", o.StopCondition)
	}
	if _, ok := reflectConditions[o.Reflect]; !ok {
		fail("--reflect must be on-steps or on-finish, not %q", o.Reflect)
	}

	// Tools
	if o.AutoApprove && o.ToolsManifestPath == "" {
		fail("--skip-confirm has no effect without --tools")
	}

	// Output
	if o.Output != "text" && o.Output != "jsonl" {
		fail("--output must be text or jsonl, not %q", o.Output)
	}

	return errors.Join(errs...)
}
